//go:build tools

// Package cli will hold the Bubble Tea v2 TUI (DESIGN.md 5.9).
//
// This file exists only to pin the Charm v2 dependencies in go.mod before any
// code imports them, so that D-06 ("Bubble Tea v2 from the first commit") is a
// fact recorded in go.sum rather than an intention. The `tools` build tag means
// it is never compiled into a binary, but `go mod tidy` still considers it and
// therefore keeps the requirements.
//
// Delete this file when the real TUI lands in M1.
package cli

import (
	_ "charm.land/bubbles/v2/help"
	_ "charm.land/bubbletea/v2"
	_ "charm.land/lipgloss/v2"
)
