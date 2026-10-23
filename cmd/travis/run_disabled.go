//go:build !eval

package main

import (
	"fmt"
	"io"

	"github.com/Kzeezee/travis/internal/core"
)

// runHeadless is absent from release builds: `run --answers` approves tool calls from a
// file, which D-10 allows only in eval builds (go build -tags eval).
func runHeadless(_ []string, _ io.Writer) error {
	return fmt.Errorf("run is only in eval builds (go build -tags eval): %w", core.ErrInvalid)
}
