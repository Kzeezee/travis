// Command travis is the composition root: wiring and flags only.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/Kzeezee/travis/internal/config"
)

const shortRevLen = 12

// Overridden at release time via: go build -ldflags "-X main.version=v0.1.0"
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

func versionString() string {
	revision, dirty := vcsStamp()

	build := "travis " + version
	switch {
	case revision != "" && dirty:
		build += " (" + revision + ", dirty)"
	case revision != "":
		build += " (" + revision + ")"
	}

	return build + "\n" + runtime.Version() + " " + config.Platform()
}

// Stamps are absent for `go run` and for builds from an archive rather than a checkout.
func vcsStamp() (revision string, dirty bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}

	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
			if len(revision) > shortRevLen {
				revision = revision[:shortRevLen]
			}
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}

	return revision, dirty
}
