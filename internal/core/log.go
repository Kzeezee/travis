package core

// Logger is the small logging facade shared across Travis module seams.
type Logger interface {
	// LogDebugMsg records low-level diagnostic details, such as selected provider or retry count.
	LogDebugMsg(msg string, attrs ...any)
	// LogInfoMsg records normal progress, such as provider connected or config loaded.
	LogInfoMsg(msg string, attrs ...any)
	// LogWarnMsg records recoverable surprises, such as dropped events or fallback config paths.
	LogWarnMsg(msg string, attrs ...any)
	// LogErrorMsg records failed required work, such as provider request or tool execution failure.
	LogErrorMsg(msg string, attrs ...any)
}
