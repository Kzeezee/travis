// Package sandbox starts a target process under an OS sandbox by way of a launcher that confines itself and then execs the target.
package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/Kzeezee/travis/internal/core"
)

// Tier names the sandbox backend that confines a process; values are shown to the user as-is.
type Tier string

const (
	TierNone            Tier = "none"
	TierLandlock        Tier = "landlock"
	TierLandlockNoNet   Tier = "landlock-no-net" // kernel older than Landlock ABI v4 (Linux 6.7): files only, no network containment
	TierSeatbelt        Tier = "seatbelt"
	TierRestrictedToken Tier = "restricted-token" // low-integrity restricted token + job object; no network containment (DESIGN §9.2.1)
)

// LauncherArg is the hidden first argument that switches a launcher binary into RunLauncher.
const LauncherArg = "__sandbox-launch"

// Spec describes the target and what it may touch. Every path must be absolute.
type Spec struct {
	// Launcher is the absolute path of an already resolved and hash-checked launcher binary.
	Launcher     string   `json:"launcher"`
	Argv         []string `json:"argv"`
	Env          []string `json:"env"`
	Dir          string   `json:"dir"`
	ReadPaths    []string `json:"read_paths"`
	WritePaths   []string `json:"write_paths"`
	AllowNetwork bool     `json:"allow_network"`
}

// BuildCommand returns a launcher command that confines itself to spec and then execs spec.Argv.
// It fails closed with core.ErrUnsupported when this OS has no sandbox backend.
func BuildCommand(ctx context.Context, spec Spec) (*exec.Cmd, Tier, error) {
	if err := validateSpec(spec); err != nil {
		return nil, TierNone, err
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return nil, TierNone, fmt.Errorf("encode sandbox spec: %w", err)
	}
	// The spec rides in argv because the target's stdin is reserved for MCP stdio.
	cmd := exec.CommandContext(ctx, spec.Launcher, LauncherArg, string(encoded)) //nolint:gosec // validateSpec checked Launcher, which the caller resolved and hash-checked
	tier, err := prepareCommandForOS(cmd, spec)
	if err != nil {
		return nil, TierNone, err
	}
	return cmd, tier, nil
}

// Code that runs inside the launcher process once it has started
func RunLauncher(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("sandbox launcher wants 1 spec argument, got %d: %w", len(args), core.ErrInvalid)
	}
	var spec Spec
	if err := json.Unmarshal([]byte(args[0]), &spec); err != nil {
		return fmt.Errorf("decode sandbox spec: %w", core.ErrInvalid)
	}
	if err := validateSpec(spec); err != nil {
		return err
	}
	if err := applyToSelf(spec); err != nil {
		return err
	}
	return execTarget(spec)
}

func validateSpec(spec Spec) error {
	if len(spec.Argv) == 0 {
		return fmt.Errorf("sandbox spec has no argv: %w", core.ErrInvalid)
	}
	paths := append([]string{spec.Launcher, spec.Dir}, spec.ReadPaths...)
	for _, path := range append(paths, spec.WritePaths...) {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("sandbox spec path %q is not absolute: %w", path, core.ErrInvalid)
		}
	}
	return nil
}
