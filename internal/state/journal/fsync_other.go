//go:build !windows

package journal

import (
	"errors"
	"os"
)

// syncDir flushes name changes in dir (create, rename, delete) to disk
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
