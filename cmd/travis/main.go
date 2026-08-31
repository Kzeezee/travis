// Command travis is the composition root for the travis assistant.
//
// Per DESIGN.md §8 this package contains wiring, flags, and nothing else. At M0
// it wires nothing, because nothing exists yet to wire; `travis version` is the
// M0 exit criterion (DESIGN.md §10) and the smoke test for the three-OS build
// matrix.
//
// No CLI framework is used here deliberately. Choosing one is a real decision
// that deserves its own ADR, and a single `version` subcommand does not justify
// prejudging it.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
)

// version is overridden at release time with:
//
//	go build -ldflags "-X main.version=v0.1.0"
//
// When it is not overridden, buildInfo() falls back to the VCS stamps the Go
// toolchain embeds automatically, so a plain `go build` still reports something
// truthful rather than a lie with a version number on it.
var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "travis:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("no command given (try: travis version)")
	}

	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintln(out, versionString())
		return nil
	default:
		return fmt.Errorf("unknown command %q (try: travis version)", args[0])
	}
}

// versionString renders the build identity. GOOS/GOARCH are included so that
// output from the three-OS CI matrix (DESIGN.md §8.1) identifies itself without
// the reader having to correlate it with the job name.
func versionString() string {
	rev, dirty := buildInfo()

	s := "travis " + version
	if rev != "" {
		s += " (" + rev
		if dirty {
			s += ", dirty"
		}
		s += ")"
	}
	return s + "\n" +
		runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH
}

// buildInfo reports the VCS revision the Go toolchain stamped into the binary,
// and whether the working tree was modified at build time. Both are absent for
// `go run` and for builds from an archive rather than a checkout.
func buildInfo() (rev string, dirty bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) > 12 {
				rev = s.Value[:12]
			} else {
				rev = s.Value
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return rev, dirty
}
