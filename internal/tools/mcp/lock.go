package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/pelletier/go-toml/v2"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
)

// lockFileName records what `travis mcp add` approved for each registry server (DESIGN.md §5.7, D-32 rule 4).
const lockFileName = "mcp.lock"

// InstallState is how a server's install compares with mcp.lock; a mismatch is an error, not a state.
type InstallState string

const (
	InstallLocal               InstallState = "local command, not from a registry"
	InstallUnverified          InstallState = "recorded, not enforced: uvx has no hash check"
	InstallAwaitingFirstLaunch InstallState = "not launched yet"
	InstallVerified            InstallState = "ok"
)

// lockedServer pins one server: Integrity is the registry's hash at add time, Resolved every package npx installed
// at first launch (trust on first use), keyed by its path in npm's lockfile.
type lockedServer struct {
	Ref       string            `toml:"ref"`
	Integrity string            `toml:"integrity"`
	Resolved  map[string]string `toml:"resolved,omitempty"`
}

type lockFile struct {
	Server map[string]lockedServer `toml:"server"`
}

// RecordInstall pins name to ref after the user approves it. Re-adding the same ref and integrity keeps
// what was resolved, so re-approving cannot launder a tampered install.
func RecordInstall(stateDir, name string, ref PackageRef, integrity string) error {
	if ref.Registry == RegistryNPM && integrity == "" {
		return fmt.Errorf("npm reported no integrity hash for %s, so it cannot be pinned: %w", ref, core.ErrInvalid)
	}
	return updateLockFile(filepath.Join(stateDir, lockFileName), func(lock *lockFile) {
		entry := lockedServer{Ref: ref.String(), Integrity: integrity}
		if previous, ok := lock.Server[name]; ok && previous.Ref == entry.Ref && previous.Integrity == entry.Integrity {
			entry.Resolved = previous.Resolved
		}
		lock.Server[name] = entry
	})
}

// ForgetInstall unpins name; forgetting one that was never pinned is not an error.
func ForgetInstall(stateDir, name string) error {
	path := filepath.Join(stateDir, lockFileName)
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return updateLockFile(path, func(lock *lockFile) { delete(lock.Server, name) })
}

// VerifyInstall reports whether name's launch still matches what `travis mcp add` approved, as `travis mcp verify` shows it.
func VerifyInstall(stateDir, name string, cfg config.MCPServerCfg) (InstallState, error) {
	return verifyInstall(filepath.Join(stateDir, lockFileName), buildScratchDir(stateDir, name), name, cfg)
}

func buildScratchDir(stateDir, name string) string {
	return filepath.Join(stateDir, "mcp", name)
}

// verifyInstall refuses a registry launch mcp.lock does not pin, and any npx install that differs from the pin.
// ponytail: checks the supply chain, not in-place tampering by the running server, which can write its scratch
// folder; upgrade path is a travis-owned `npm ci` install mounted read-only (plan TRA-71 §1.1 option C).
func verifyInstall(lockPath, scratchDir, name string, cfg config.MCPServerCfg) (InstallState, error) {
	ref, isRegistry, err := ResolvePackageRef(cfg.Command, cfg.Args)
	if err != nil {
		return "", err
	}
	if !isRegistry {
		return InstallLocal, nil
	}
	lock, err := readLockFile(lockPath)
	if err != nil {
		return "", err
	}
	locked, isLocked := lock.Server[name]
	switch {
	case !isLocked:
		return "", fmt.Errorf("mcp server %q runs %s, which mcp.lock does not pin; add it with travis mcp add: %w", name, ref, core.ErrDenied)
	case locked.Ref != ref.String():
		return "", fmt.Errorf("mcp server %q runs %s, but travis mcp add approved %s; add it again to approve the change: %w", name, ref, locked.Ref, core.ErrDenied)
	case ref.Registry != RegistryNPM:
		return InstallUnverified, nil
	}

	installs, err := readNPXInstalls(scratchDir, ref)
	if err != nil {
		return "", fmt.Errorf("mcp server %q: %w", name, err)
	}
	if len(installs) == 0 {
		return InstallAwaitingFirstLaunch, nil
	}
	isFirstLaunch := len(locked.Resolved) == 0
	for _, installed := range installs {
		if root := installed[npmPackagePath(ref.Name)]; root != locked.Integrity {
			return "", fmt.Errorf("mcp server %q: npx installed %s with integrity %q, not the %q travis mcp add approved: %w", name, ref, root, locked.Integrity, core.ErrDenied)
		}
		if isFirstLaunch {
			locked.Resolved = installed
		}
		if changed := compareResolved(locked.Resolved, installed); len(changed) > 0 {
			return "", fmt.Errorf("mcp server %q: installed packages differ from mcp.lock: %v: %w", name, changed, core.ErrDenied)
		}
	}
	if isFirstLaunch {
		err = updateLockFile(lockPath, func(lock *lockFile) {
			if current, ok := lock.Server[name]; ok && current.Ref == locked.Ref && len(current.Resolved) == 0 {
				lock.Server[name] = locked
			}
		})
	}
	return InstallVerified, err
}

// compareResolved reports, sorted, every package path whose integrity differs or that only one side has.
func compareResolved(locked, installed map[string]string) []string {
	var changed []string
	for path, want := range locked {
		if got, ok := installed[path]; !ok || got != want {
			changed = append(changed, path)
		}
	}
	for path := range installed {
		if _, ok := locked[path]; !ok {
			changed = append(changed, path)
		}
	}
	slices.Sort(changed)
	return changed
}

func npmPackagePath(name string) string {
	return "node_modules/" + name
}

// readNPXInstalls reads every folder npx installed ref into under scratchDir's npm cache (buildScratchEnv), as
// path → integrity; a bundled package has no integrity of its own and is covered by its parent's.
func readNPXInstalls(scratchDir string, ref PackageRef) ([]map[string]string, error) {
	lockfiles, err := filepath.Glob(filepath.Join(scratchDir, "npm-cache", "_npx", "*", "package-lock.json"))
	if err != nil {
		return nil, err
	}
	var installs []map[string]string
	for _, lockfile := range lockfiles {
		data, err := os.ReadFile(lockfile)
		if err != nil {
			return nil, fmt.Errorf("read what npx installed: %w", err)
		}
		var npmLock struct {
			Packages map[string]struct {
				Version   string `json:"version"`
				Integrity string `json:"integrity"`
			} `json:"packages"`
		}
		if err := json.Unmarshal(data, &npmLock); err != nil {
			return nil, fmt.Errorf("read %s: %w", lockfile, core.ErrDenied)
		}
		if npmLock.Packages[npmPackagePath(ref.Name)].Version != ref.Version {
			continue
		}
		installed := map[string]string{}
		for path, pkg := range npmLock.Packages {
			if path == "" {
				continue
			}
			installed[path] = pkg.Integrity
			if pkg.Integrity == "" {
				installed[path] = "bundled " + pkg.Version
			}
		}
		installs = append(installs, installed)
	}
	return installs, nil
}

// readLockFile fails closed: an unreadable lock refuses every registry launch rather than trusting nothing changed.
func readLockFile(path string) (lockFile, error) {
	lock := lockFile{Server: map[string]lockedServer{}}
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return lock, nil
	}
	err := withLockedFile(path, func(file *os.File) error {
		var readErr error
		lock, readErr = decodeLockFile(file)
		return readErr
	})
	return lock, err
}

// updateLockFile is a read-modify-write under an exclusive lock, so concurrent sessions and `travis mcp add` never lose an entry.
func updateLockFile(path string, change func(*lockFile)) error {
	if err := os.MkdirAll(filepath.Dir(path), privateStateDirMode); err != nil {
		return fmt.Errorf("mcp.lock: %w", err)
	}
	return withLockedFile(path, func(file *os.File) error {
		lock, err := decodeLockFile(file)
		if err != nil {
			return err
		}
		change(&lock)
		encoded, err := toml.Marshal(lock)
		if err != nil {
			return fmt.Errorf("mcp.lock: encode: %w", err)
		}
		if err := file.Truncate(0); err != nil {
			return fmt.Errorf("mcp.lock: %w", err)
		}
		if _, err := file.WriteAt(encoded, 0); err != nil {
			return fmt.Errorf("mcp.lock: write: %w", err)
		}
		return file.Sync()
	})
}

// withLockedFile holds the lock for readers too: writers truncate in place, so an unlocked read could see half a file.
func withLockedFile(path string, use func(*os.File) error) error {
	file, err := openPrivateFileNoFollow(path)
	if err != nil {
		return fmt.Errorf("mcp.lock: %w", err)
	}
	defer file.Close()
	if err := lockExclusive(file); err != nil {
		return fmt.Errorf("mcp.lock: lock: %w", err)
	}
	defer func() { _ = unlockExclusive(file) }()
	return use(file)
}

func decodeLockFile(file *os.File) (lockFile, error) {
	lock := lockFile{Server: map[string]lockedServer{}}
	data, err := io.ReadAll(file)
	if err != nil {
		return lock, fmt.Errorf("mcp.lock: read: %w", err)
	}
	if err := toml.Unmarshal(data, &lock); err != nil {
		return lock, fmt.Errorf("mcp.lock is not valid TOML, so no registry server can be verified; fix or delete it and add the servers again: %w", core.ErrDenied)
	}
	if lock.Server == nil {
		lock.Server = map[string]lockedServer{}
	}
	return lock, nil
}
