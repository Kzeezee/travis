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

// LocalBroker does direct IO in the calling process, on paths its access rule allows. The rule
// is checked on the path as written and again once symlinks are followed, so a link cannot
// lead somewhere the rule refuses.
type LocalBroker struct {
	paths config.PathSet
	root  string
	// resolvedRoot is root with symlinks evaluated, so confinement compares like with like.
	resolvedRoot string
	// authorizeRead returns an error wrapping core.ErrDenied to refuse reading an absolute path.
	authorizeRead func(path string) error
}

var _ core.Broker = (*LocalBroker)(nil)

// BuildLocalBroker is the Phase 1 broker: confined to one workspace, with no policy (D-18).
func BuildLocalBroker(workspaceDir string) *LocalBroker {
	b := buildLocalBrokerForWorkspace(workspaceDir)
	b.authorizeRead = func(path string) error {
		// Either form of the root is accepted, since paths this broker reported back are resolved.
		if isInsideRoot(b.root, path) || isInsideRoot(b.resolvedRoot, path) {
			return nil
		}
		return fmt.Errorf("%q is outside the workspace %q: %w", path, b.root, core.ErrDenied)
	}
	return b
}

func buildLocalBrokerForWorkspace(workspaceDir string) *LocalBroker {
	root := filepath.Clean(workspaceDir)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolvedRoot = root
	}
	return &LocalBroker{paths: config.PathSet{WorkspaceDir: root}, root: root, resolvedRoot: resolvedRoot}
}

func (b *LocalBroker) DescribePath(ctx context.Context, path string) (core.PathInfo, error) {
	if err := ctx.Err(); err != nil {
		return core.PathInfo{}, err
	}
	resolved, err := b.resolveReadablePath(path)
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
	resolved, err := b.resolveReadablePath(path)
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

// resolveReadablePath turns a requested path into a real path on disk, allowed only if the
// access rule accepts it both as written and once symlinks are followed.
func (b *LocalBroker) resolveReadablePath(requested string) (string, error) {
	resolved, err := b.paths.Resolve(requested)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(resolved) {
		return "", fmt.Errorf("%q does not resolve to an absolute path: %w", requested, core.ErrDenied)
	}
	// Checking before symlinks are followed keeps a refused path from revealing whether it exists.
	if err := b.authorizeRead(resolved); err != nil {
		return "", err
	}
	evaluated, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", translatePathError(requested, err)
	}
	if err := b.authorizeRead(evaluated); err != nil {
		return "", err
	}
	return evaluated, nil
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
