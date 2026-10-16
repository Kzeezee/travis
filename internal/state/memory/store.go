package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Kzeezee/travis/internal/core"
)

const (
	MaxBytes       = 8 << 10
	MaxLines       = 200
	FileName       = "memory.md"
	trustedHeader  = "# Memory\n\n## Trusted notes\n"
	untrustedStart = "<!-- BEGIN UNTRUSTED -->"
	untrustedEnd   = "<!-- END UNTRUSTED -->"
)

type Store struct {
	path string
}

func BuildStore(stateDir string) *Store {
	return &Store{path: filepath.Join(stateDir, FileName)}
}

func (s *Store) ReadMemory(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return renderMemory("", ""), nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("memory path is not a regular file: %w", core.ErrDenied)
	}
	content, err := os.ReadFile(s.path)
	if err != nil {
		return "", err
	}
	if err := validateMemory(string(content)); err != nil {
		return "", err
	}
	return string(content), nil
}

func (s *Store) ReplaceMemory(ctx context.Context, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !utf8.ValidString(content) || strings.Contains(content, untrustedStart) || strings.Contains(content, untrustedEnd) {
		return fmt.Errorf("memory content contains invalid text or a reserved delimiter: %w", core.ErrInvalid)
	}
	current, err := s.ReadMemory(ctx)
	if err != nil {
		return err
	}
	trusted, untrusted, err := splitMemory(current)
	if err != nil {
		return err
	}
	if core.ReportIsFromUntrustedStep(ctx) {
		untrusted = strings.TrimSpace(content)
	} else {
		trusted = strings.TrimSpace(content)
	}
	next := renderMemory(trusted, untrusted)
	if err := validateMemory(next); err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".memory-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.WriteString(next); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, s.path)
}

func renderMemory(trusted, untrusted string) string {
	return trustedHeader + trusted + "\n" + untrustedStart + "\n" + untrusted + "\n" + untrustedEnd + "\n"
}

func splitMemory(content string) (string, string, error) {
	rest, ok := strings.CutPrefix(content, trustedHeader)
	if !ok {
		return "", "", fmt.Errorf("memory header is missing: %w", core.ErrInvalid)
	}
	trusted, rest, ok := strings.Cut(rest, "\n"+untrustedStart+"\n")
	if !ok {
		return "", "", fmt.Errorf("untrusted memory section is missing: %w", core.ErrInvalid)
	}
	untrusted, tail, ok := strings.Cut(rest, "\n"+untrustedEnd+"\n")
	if !ok || tail != "" {
		return "", "", fmt.Errorf("untrusted memory section is malformed: %w", core.ErrInvalid)
	}
	return trusted, untrusted, nil
}

// ReportHasUntrustedNotes reports whether content, as ReadMemory returned it, carries any
// untrusted-derived notes; malformed content counts as untrusted.
func ReportHasUntrustedNotes(content string) bool {
	_, untrusted, err := splitMemory(content)
	return err != nil || strings.TrimSpace(untrusted) != ""
}

func validateMemory(content string) error {
	if len(content) > MaxBytes || strings.Count(content, "\n") > MaxLines || !utf8.ValidString(content) {
		return fmt.Errorf("memory exceeds %d bytes or %d lines, or is not UTF-8: %w", MaxBytes, MaxLines, core.ErrInvalid)
	}
	_, _, err := splitMemory(content)
	return err
}
