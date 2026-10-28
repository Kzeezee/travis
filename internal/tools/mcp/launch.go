package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/sandbox"
)

const (
	scratchDirMode = 0o700
	stderrLogName  = "stderr.log"
	stderrLogMode  = 0o600
)

// Sandboxing is how one session confines its MCP servers (DESIGN.md §9.2, strict tier).
type Sandboxing struct {
	// ResolveLauncher reports the launcher binary, verified afresh before every launch; nil refuses every launch.
	ResolveLauncher func() (string, error)
	// IsRequired refuses a server this OS cannot sandbox; false runs it unconfined at TierNone.
	IsRequired bool
}

// launchGrant is what a sandboxed launch may touch; trust is remembered per grant, so widening it asks again.
type launchGrant struct {
	Tier         sandbox.Tier
	AllowNetwork bool
	ReadPaths    []string
	WritePaths   []string
}

// buildLaunchCommand confines the configured server, or refuses it when no sandbox exists and one is required.
func (p *serverProcess) buildLaunchCommand(ctx context.Context) (*exec.Cmd, launchGrant, error) {
	grant := launchGrant{Tier: sandbox.TierNone, AllowNetwork: p.cfg.AllowNet, ReadPaths: p.cfg.ReadPaths, WritePaths: p.cfg.WritePaths}
	// resolveServerCommand and Args come only from the user's own trusted config, never from model output.
	executable, err := exec.LookPath(resolveServerCommand(p.cfg.Command))
	if err != nil {
		return nil, grant, fmt.Errorf("mcp server %q: find %q: %w", p.name, p.cfg.Command, errors.Join(core.ErrNotFound, err))
	}
	if executable, err = filepath.Abs(executable); err != nil {
		return nil, grant, fmt.Errorf("mcp server %q: %w", p.name, err)
	}
	if p.sandboxing.ResolveLauncher == nil {
		return nil, grant, fmt.Errorf("mcp server %q not started: no sandbox launcher is available: %w", p.name, core.ErrDenied)
	}
	launcher, err := p.sandboxing.ResolveLauncher()
	if err != nil {
		return nil, grant, fmt.Errorf("mcp server %q not started: %w", p.name, err)
	}
	if err := os.MkdirAll(filepath.Join(p.scratchDir, "tmp"), scratchDirMode); err != nil {
		return nil, grant, fmt.Errorf("mcp server %q: create scratch folder: %w", p.name, err)
	}

	cmd, tier, err := p.buildSandboxedCommand(ctx, buildSandboxSpec(launcher, executable, p.scratchDir, p.cfg))
	switch {
	case err == nil:
		grant.Tier = tier
		return cmd, grant, nil
	case errors.Is(err, core.ErrUnsupported) && !p.sandboxing.IsRequired:
		// The user turned off require_sandbox; command and args come from their own config.
		cmd = exec.CommandContext(ctx, executable, p.cfg.Args...)
		cmd.Env = buildServerEnv(p.cfg.Env)
		return cmd, grant, nil
	case errors.Is(err, core.ErrUnsupported):
		return nil, grant, fmt.Errorf("mcp server %q not started: %w; set [security] require_sandbox = false in your user config to run it unsandboxed", p.name, err)
	default:
		return nil, grant, fmt.Errorf("mcp server %q: %w", p.name, err)
	}
}

// createStderrLog starts a fresh log for one launch. The sandboxed server can write its scratch folder, so it could
// plant a symlink at this name; removing it and then creating with O_EXCL never follows one out of scratch.
// shortcut: each launch replaces the log, so a restart loses the crash's output; keep one per launch if that matters.
func createStderrLog(scratchDir string) (*os.File, error) {
	path := filepath.Join(scratchDir, stderrLogName)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("replace stderr log: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, stderrLogMode)
	if err != nil {
		return nil, fmt.Errorf("create stderr log: %w", err)
	}
	return file, nil
}

// buildSandboxSpec gives the server its runner's install folder to read and only its own scratch folder to write.
func buildSandboxSpec(launcher, executable, scratchDir string, cfg config.MCPServerCfg) sandbox.Spec {
	return sandbox.Spec{
		Launcher:     launcher,
		Argv:         append([]string{executable}, cfg.Args...),
		Env:          buildServerEnv(buildScratchEnv(scratchDir), cfg.Env),
		Dir:          scratchDir,
		ReadPaths:    slices.Concat(sandbox.ResolveRunnerReadPaths(executable), cfg.ReadPaths),
		WritePaths:   slices.Concat([]string{scratchDir}, cfg.WritePaths),
		AllowNetwork: cfg.AllowNet,
	}
}

// buildScratchEnv points home, temp and package caches into scratch, so npx and uvx never write outside it;
// prefer_offline lets a warm npm cache start with no network instead of waiting for the registry to time out.
// APPDATA and LOCALAPPDATA move too, because Windows tools such as uv keep their data there rather than in home.
func buildScratchEnv(scratchDir string) map[string]string {
	tmp := filepath.Join(scratchDir, "tmp")
	return map[string]string{
		"HOME": scratchDir, "USERPROFILE": scratchDir, "APPDATA": scratchDir, "LOCALAPPDATA": scratchDir,
		"TMPDIR": tmp, "TEMP": tmp, "TMP": tmp,
		"XDG_CACHE_HOME":            filepath.Join(scratchDir, "cache"),
		"npm_config_cache":          filepath.Join(scratchDir, "npm-cache"),
		"npm_config_prefer_offline": "true",
		"UV_CACHE_DIR":              filepath.Join(scratchDir, "uv-cache"),
	}
}

// buildServerEnv layers env maps over travis's own secret-free environment, later layers winning. Keys are
// unique because the sandboxed target is exec'd with this list as-is, where the first duplicate wins.
func buildServerEnv(layers ...map[string]string) []string {
	env := config.BuildEnvironmentWithoutSecrets()
	for _, layer := range layers {
		for key, value := range layer {
			env = append(env, key+"="+value)
		}
	}
	seen := map[string]bool{}
	unique := make([]string, 0, len(env))
	for _, entry := range slices.Backward(env) {
		key, _, _ := strings.Cut(entry, "=")
		if !seen[key] {
			seen[key] = true
			unique = append(unique, entry)
		}
	}
	slices.Reverse(unique)
	return unique
}
