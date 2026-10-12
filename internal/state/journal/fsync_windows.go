//go:build windows

package journal

// syncDir does nothing: Windows can't flush a folder that's open read-only, and NTFS already journals name changes.
func syncDir(string) error { return nil }
