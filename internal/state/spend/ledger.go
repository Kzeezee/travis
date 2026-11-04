// Package spend keeps the day's model spend in the state dir so every session on the machine shares one total.
package spend

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

const dayFileLayout = "2006-01-02"

// Ledger appends each spend as one decimal line to a file per local day.
// ponytail: O_APPEND line writes are the only cross-session sync (D-32); add a lock if a torn line is ever seen.
type Ledger struct {
	dir   string
	clock core.Clock
}

// BuildLedger keeps its day files under dir, which it creates on the first write.
func BuildLedger(dir string, clock core.Clock) *Ledger {
	return &Ledger{dir: dir, clock: clock}
}

func (l *Ledger) RecordSpend(_ context.Context, usd float64) error {
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return fmt.Errorf("create spend dir: %w", err)
	}
	file, err := os.OpenFile(l.buildTodayPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open spend file: %w", err)
	}
	_, writeErr := file.WriteString(strconv.FormatFloat(usd, 'f', -1, 64) + "\n")
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return fmt.Errorf("record spend: %w", writeErr)
	}
	return nil
}

// ReportSpentToday sums today's lines; a line that does not parse counts as 0 rather than failing the sum.
func (l *Ledger) ReportSpentToday(_ context.Context) (float64, error) {
	file, err := os.Open(l.buildTodayPath())
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open spend file: %w", err)
	}
	defer func() { _ = file.Close() }()

	var total float64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if usd, parseErr := strconv.ParseFloat(strings.TrimSpace(scanner.Text()), 64); parseErr == nil && usd > 0 {
			total += usd
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read spend file: %w", err)
	}
	return total, nil
}

func (l *Ledger) buildTodayPath() string {
	return filepath.Join(l.dir, l.clock.ReportCurrentTime().Local().Format(dayFileLayout)+".log")
}
