//go:build tools

// Pins the Charm v2 dependencies (D-06) until the real TUI imports them in M1; delete then.
package cli

import (
	_ "charm.land/bubbles/v2/help"
	_ "charm.land/bubbletea/v2"
	_ "charm.land/lipgloss/v2"
)
