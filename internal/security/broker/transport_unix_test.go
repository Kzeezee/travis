//go:build linux || darwin

package broker_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
)

func TestCreateSessionEndpointMakesAPrivateDirectoryPerSession(t *testing.T) {
	runtimeDir := buildShortRuntimeDir(t)
	first, removeFirst, err := broker.CreateSessionEndpoint(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionDir := filepath.Dir(string(first))
	info, err := os.Stat(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("session directory %q has mode %v, want 0700", sessionDir, perm)
	}
	removeFirst()
	if _, err := os.Stat(sessionDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session directory still exists after removal: %v", err)
	}
}

func TestCreateSessionEndpointRefusesSocketPathsOverTheOSLimit(t *testing.T) {
	runtimeDir := filepath.Join(buildShortRuntimeDir(t), strings.Repeat("d", 100))
	if _, _, err := broker.CreateSessionEndpoint(runtimeDir); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("CreateSessionEndpoint(long dir) error = %v, want ErrInvalid", err)
	}
	if entries, _ := os.ReadDir(runtimeDir); len(entries) != 0 {
		t.Fatalf("a refused endpoint left %v behind", entries)
	}
}
