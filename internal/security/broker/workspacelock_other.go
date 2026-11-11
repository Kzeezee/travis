//go:build !linux && !darwin && !windows

package broker

// isProcessAlive has no implementation on this OS, so every marker reads as stale: claiming a
// workspace still records this session's PID, it just never warns about another one (§8.1).
func isProcessAlive(int) bool {
	return false
}
