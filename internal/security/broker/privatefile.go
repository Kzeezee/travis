package broker

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Kzeezee/travis/internal/core"
)

// Shared by every workspace-level file under .travis/ that only the user may read: currently
// decisions.toml and session.lock.
const (
	privateStateDirMode  = 0o700
	privateStateFileMode = 0o600
)

// openPrivateFileNoFollow opens path for read-write without ever following a symlink planted at
// its directory or at the file itself: a cloned repo could aim .travis, or .travis/<name>, at
// something like ~/.bashrc, and a naive open would write there instead (R-26). The caller decides
// what to do with the file's existing content — append, truncate, or read it first.
func openPrivateFileNoFollow(path string) (*os.File, error) {
	dir := filepath.Dir(path)
	if err := os.Mkdir(dir, privateStateDirMode); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	if info, err := os.Lstat(dir); err != nil {
		return nil, err
	} else if !info.IsDir() {
		return nil, fmt.Errorf("%q is not a real folder: %w", dir, core.ErrDenied)
	}

	// O_EXCL refuses anything already at the name, a symlink included, on every OS.
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, privateStateFileMode)
	if !errors.Is(err, fs.ErrExist) {
		return file, err
	}
	existing, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !existing.Mode().IsRegular() {
		return nil, fmt.Errorf("%q is not a regular file: %w", path, core.ErrDenied)
	}
	if file, err = os.OpenFile(path, os.O_RDWR, 0); err != nil {
		return nil, err
	}
	// The name could have been swapped for a link between Lstat and open; check what was opened.
	if opened, err := file.Stat(); err != nil || !os.SameFile(existing, opened) {
		_ = file.Close()
		return nil, fmt.Errorf("%q changed while it was being opened: %w", path, core.ErrDenied)
	}
	return file, nil
}
