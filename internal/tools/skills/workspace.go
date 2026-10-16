package skills

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
// as it would be added, so approving it approves those bytes and not whatever is on disk later.
type workspaceContent struct {
	dir         string
	skills      map[string]skill
	agentsBlock string
	hash        string
}

func (w workspaceContent) isEmpty() bool {
	return len(w.skills) == 0 && w.agentsBlock == ""
}

func (w workspaceContent) reportSortedSkillNames() []string {
	names := make([]string, 0, len(w.skills))
	for name := range w.skills {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// reportHash is a SHA-256 over every field the prompt would carry, length-prefixed so no two
// different contents can serialize alike.
func (w workspaceContent) reportHash() string {
	digest := sha256.New()
	write := func(field string) { fmt.Fprintf(digest, "%d:%s", len(field), field) }
	write(w.agentsBlock)
	for _, name := range w.reportSortedSkillNames() {
		s := w.skills[name]
		write(s.Name)
		write(s.Description)
		write(s.Body)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// describe names what approval would load, for the human deciding.
func (w workspaceContent) describe() string {
	var parts []string
	if w.agentsBlock != "" {
		parts = append(parts, agentsFileName)
	}
	if len(w.skills) > 0 {
		parts = append(parts, "workspace skills "+strings.Join(w.reportSortedSkillNames(), ", "))
	}
	return strings.Join(parts, " and ")
}

// isWorkspaceContentTrusted reports whether hash was approved "always"; an unreadable file
// fails safe toward asking again.
func isWorkspaceContentTrusted(path, hash string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	lines := bufio.NewScanner(file)
	for lines.Scan() {
		if strings.TrimSpace(lines.Text()) == hash {
			return true
		}
	}
	return false
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
