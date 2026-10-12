// Package broker performs effects on behalf of tools and owns the frozen wire protocol the
// broker process speaks. LocalBroker performs effects in the calling process; RemoteBroker
// forwards them to a broker process, which enforces policy before acting (D-18).
package broker

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
)

// LocalBroker does direct IO in the calling process, on paths its access rules allow. A rule
// is checked on the path as written and again once symlinks are followed, so a link cannot
// lead somewhere the rule refuses.
type LocalBroker struct {
	paths config.PathSet
	root  string
	// resolvedRoot is root with symlinks evaluated, so confinement compares like with like.
	resolvedRoot string
	// authorizeRead and authorizeWrite refuse an absolute path with an error wrapping
	// core.ErrDenied or a *core.ApprovalRequiredError. Workspace confinement is the default rule;
	// BuildPolicyEnforcedBroker replaces it with the session policy.
	authorizeRead  func(ctx context.Context, path string) error
	authorizeWrite func(ctx context.Context, path string) error
	// clock stamps AddDecisionRule's AddedAt; injected so tests can use a FixedClock.
	clock core.Clock
}

var _ core.Broker = (*LocalBroker)(nil)

// BuildLocalBroker is the Phase 1 broker: confined to one workspace, with no policy (D-18).
func BuildLocalBroker(workspaceDir string, clock core.Clock) *LocalBroker {
	b := buildLocalBrokerForWorkspace(workspaceDir, clock)
	b.authorizeRead, b.authorizeWrite = b.confineToWorkspace, b.confineToWorkspace
	return b
}

func buildLocalBrokerForWorkspace(workspaceDir string, clock core.Clock) *LocalBroker {
	root := filepath.Clean(workspaceDir)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolvedRoot = root
	}
	return &LocalBroker{paths: config.PathSet{WorkspaceDir: root}, root: root, resolvedRoot: resolvedRoot, clock: clock}
}

// confineToWorkspace accepts either form of the root, since paths this broker reported back
// are already resolved.
func (b *LocalBroker) confineToWorkspace(_ context.Context, path string) error {
	if isInsideRoot(b.root, path) || isInsideRoot(b.resolvedRoot, path) {
		return nil
	}
	return b.reportOutsideWorkspace(path)
}

func (b *LocalBroker) DescribePath(ctx context.Context, path string) (core.PathInfo, error) {
	if err := ctx.Err(); err != nil {
		return core.PathInfo{}, err
	}
	resolved, err := b.resolveReadablePath(ctx, path)
	if err != nil {
		return core.PathInfo{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return core.PathInfo{}, translatePathError(path, err)
	}
	return b.describeResolvedPath(resolved, info.Mode()), nil
}

func (b *LocalBroker) ListDirectory(ctx context.Context, path string) ([]core.PathInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resolved, err := b.resolveReadablePath(ctx, path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return nil, translatePathError(path, err)
	}
	listed := make([]core.PathInfo, 0, len(entries))
	for _, entry := range entries {
		listed = append(listed, b.describeResolvedPath(filepath.Join(resolved, entry.Name()), entry.Type()))
	}
	return listed, nil
}

func (b *LocalBroker) OpenFile(ctx context.Context, path string) (core.OpenedFile, error) {
	info, err := b.DescribePath(ctx, path)
	if err != nil {
		return core.OpenedFile{}, err
	}
	switch info.Kind {
	case core.PathKindFile:
	case core.PathKindDirectory:
		return core.OpenedFile{}, fmt.Errorf("%q: %w", path, core.ErrIsDirectory)
	case core.PathKindSymlink, core.PathKindOther:
		return core.OpenedFile{}, fmt.Errorf("%q is not a regular file: %w", path, core.ErrInvalid)
	}
	file, err := os.Open(info.Path)
	if err != nil {
		return core.OpenedFile{}, translatePathError(path, err)
	}
	return core.OpenedFile{Info: info, Content: file}, nil
}

func (b *LocalBroker) WriteFile(ctx context.Context, path string, content []byte) (core.PathInfo, error) {
	if err := ctx.Err(); err != nil {
		return core.PathInfo{}, err
	}
	target, err := b.resolveWritablePath(ctx, path)
	if err != nil {
		return core.PathInfo{}, err
	}
	perm := fs.FileMode(0o644)
	existing, err := os.Lstat(target)
	switch {
	case err == nil && existing.IsDir():
		return core.PathInfo{}, fmt.Errorf("%q: %w", path, core.ErrIsDirectory)
	case err == nil && !existing.Mode().IsRegular():
		return core.PathInfo{}, fmt.Errorf("%q is not a regular file: %w", path, core.ErrInvalid)
	case err == nil:
		perm = existing.Mode().Perm()
	case !errors.Is(err, fs.ErrNotExist):
		return core.PathInfo{}, translatePathError(path, err)
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return core.PathInfo{}, translatePathError(path, err)
	}
	if err := replaceFileAtomically(parent, target, content, perm); err != nil {
		return core.PathInfo{}, translatePathError(path, err)
	}
	return b.describeResolvedPath(target, perm), nil
}

// replaceFileAtomically writes to a sibling temp file and renames it over target, so readers
// see either the old bytes or the new ones, never a partial write.
func replaceFileAtomically(dir, target string, content []byte, perm fs.FileMode) error {
	temp, err := os.CreateTemp(dir, ".travis-write-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	isRenamed := false
	defer func() {
		if !isRenamed {
			_ = os.Remove(tempPath)
		}
	}()
	_, err = temp.Write(content)
	if err == nil {
		err = temp.Chmod(perm)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tempPath, target); err != nil {
		return err
	}
	isRenamed = true
	syncDirectory(dir)
	return nil
}

// syncDirectory persists the rename; some platforms (Windows) cannot sync a directory, and the
// rename has already happened, so a failure here is not worth reporting.
func syncDirectory(dir string) {
	handle, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = handle.Sync()
	_ = handle.Close()
}

// resolveReadablePath turns a requested path into a real path on disk, allowed only if the
// read rule accepts it both as written and once symlinks are followed.
func (b *LocalBroker) resolveReadablePath(ctx context.Context, requested string) (string, error) {
	resolved, err := b.resolveAuthorizedAsWritten(ctx, requested, b.authorizeRead)
	if err != nil {
		return "", err
	}
	evaluated, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", translatePathError(requested, err)
	}
	if err := b.authorizeRead(ctx, evaluated); err != nil {
		return "", err
	}
	return evaluated, nil
}

// resolveWritablePath is resolveReadablePath for a target that may not exist yet: it follows
// symlinks only up to the nearest existing ancestor and never through the final name.
func (b *LocalBroker) resolveWritablePath(ctx context.Context, requested string) (string, error) {
	resolved, err := b.resolveAuthorizedAsWritten(ctx, requested, b.authorizeWrite)
	if err != nil {
		return "", err
	}
	ancestor, missingTail := filepath.Dir(resolved), filepath.Base(resolved)
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", translatePathError(requested, err)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", translatePathError(requested, fs.ErrNotExist)
		}
		missingTail = filepath.Join(filepath.Base(ancestor), missingTail)
		ancestor = parent
	}
	evaluatedAncestor, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", translatePathError(requested, err)
	}
	target := filepath.Join(evaluatedAncestor, missingTail)
	if err := b.authorizeWrite(ctx, target); err != nil {
		return "", err
	}
	return target, nil
}

// resolveAuthorizedAsWritten checks the path before any symlink is followed, so a refused path
// cannot reveal whether it exists.
func (b *LocalBroker) resolveAuthorizedAsWritten(ctx context.Context, requested string, authorize func(context.Context, string) error) (string, error) {
	resolved, err := b.paths.Resolve(requested)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(resolved) {
		return "", fmt.Errorf("%q does not resolve to an absolute path: %w", requested, core.ErrDenied)
	}
	// travis names the workspace as the user reached it, perhaps through a symlink; rules are
	// anchored at the resolved root, so a path under the named root moves there first.
	if b.root != b.resolvedRoot && isInsideRoot(b.root, resolved) {
		rel, _ := filepath.Rel(b.root, resolved) // isInsideRoot already proved Rel succeeds
		resolved = filepath.Join(b.resolvedRoot, rel)
	}
	if err := authorize(ctx, resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func (b *LocalBroker) reportOutsideWorkspace(requested string) error {
	return fmt.Errorf("%q is outside the workspace %q: %w", requested, b.root, core.ErrDenied)
}

func (b *LocalBroker) describeResolvedPath(path string, mode fs.FileMode) core.PathInfo {
	workspacePath := path
	if rel, err := filepath.Rel(b.resolvedRoot, path); err == nil {
		workspacePath = filepath.ToSlash(rel)
	}
	return core.PathInfo{Path: path, WorkspacePath: workspacePath, Kind: reportPathKind(mode)}
}

func reportPathKind(mode fs.FileMode) core.PathKind {
	switch {
	case mode&fs.ModeSymlink != 0:
		return core.PathKindSymlink
	case mode.IsDir():
		return core.PathKindDirectory
	case mode.IsRegular():
		return core.PathKindFile
	default:
		return core.PathKindOther
	}
}

// translatePathError keeps the OS error as the cause but classifies it with a core kind.
func translatePathError(requested string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%q: %w", requested, errors.Join(core.ErrNotFound, err))
	}
	return fmt.Errorf("%q: %w", requested, err)
}

func isInsideRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
