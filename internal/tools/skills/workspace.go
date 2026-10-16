package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// workspaceTrustFileName holds one approved content hash per line, in the user's state dir,
// which the model can never write (it is on the deny-list).
const workspaceTrustFileName = "workspace-trust"

// workspaceContent is every workspace-authored text travis would add to the prompt, exactly
// as it would be added, and where it came from: approval covers those bytes in that folder.
type workspaceContent struct {
	dir         string
	skills      map[string]skill
	agentsBlock string
	hash        string
}

func (w workspaceContent) isEmpty() bool {
	return len(w.skills) == 0 && w.agentsBlock == ""
}

// reportHash covers the workspace folder, each skill's folder and every byte the prompt would
// carry, so an edit or the same text at another path asks again. JSON sorts the map keys.
func (w workspaceContent) reportHash() string {
	data, _ := json.Marshal(struct {
		Dir, AgentsBlock string
		Skills           map[string]skill
	}{w.dir, w.agentsBlock, w.skills})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// describe names what approval would load, for the human deciding.
func (w workspaceContent) describe() string {
	var parts []string
	if w.agentsBlock != "" {
		parts = append(parts, agentsFileName)
	}
	if len(w.skills) > 0 {
		parts = append(parts, "workspace skills "+strings.Join(slices.Sorted(maps.Keys(w.skills)), ", "))
	}
	return strings.Join(parts, " and ")
}

// isWorkspaceContentTrusted reports whether hash was approved "always"; an unreadable file
// fails safe toward asking again.
func isWorkspaceContentTrusted(path, hash string) bool {
	data, _ := os.ReadFile(path)
	return slices.Contains(strings.Fields(string(data)), hash)
}

// trustWorkspaceContent appends hash as one short O_APPEND write, which concurrent sessions
// cannot interleave.
func trustWorkspaceContent(path, hash string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(hash + "\n"); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
