package skills

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// maxInstructionFileBytes caps one AGENTS.md or SKILL.md; prompt text this size is already absurd.
const maxInstructionFileBytes = 256 << 10

// readInstructionFile reads a regular file only, so a cloned repo's symlink can't pull a
// deny-listed file (~/.ssh/...) or an endless one (/dev/zero) into the prompt.
func readInstructionFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file; symlinks and devices are refused")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if opened, err := file.Stat(); err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("changed while it was being opened")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxInstructionFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxInstructionFileBytes {
		return nil, fmt.Errorf("larger than %d bytes", maxInstructionFileBytes)
	}
	return data, nil
}
