package log

import (
	"io"
	"log/slog"
)

// Level names the minimum severity a built logger should record.
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

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

// slogLogger adapts Go's structured logger to the Travis Logger interface.
type slogLogger struct {
	// inner is the wrapped standard-library structured logger.
	inner *slog.Logger
}

// BuildDefaultLogger wraps Go's default structured logger.
func BuildDefaultLogger() Logger {
	return slogLogger{inner: slog.Default()}
}

// BuildTextLogger builds a structured text logger that writes to the provided writer.
func BuildTextLogger(out io.Writer, level Level) Logger {
	if out == nil {
		out = io.Discard
	}
	logger := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: selectSlogLevel(level)}))
	return slogLogger{inner: logger}
}

// BuildNopLogger builds a logger that discards all log output.
func BuildNopLogger() Logger {
	return BuildTextLogger(io.Discard, LevelError)
}

func selectSlogLevel(level Level) slog.Level {
	switch level {
	case LevelDebug:
		return slog.LevelDebug
	case LevelInfo:
		return slog.LevelInfo
	case LevelWarn:
		return slog.LevelWarn
	case LevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// LogDebugMsg forwards a debug message and structured attributes to slog.
func (l slogLogger) LogDebugMsg(msg string, attrs ...any) {
	l.inner.Debug(msg, attrs...)
}

// LogInfoMsg forwards an info message and structured attributes to slog.
func (l slogLogger) LogInfoMsg(msg string, attrs ...any) {
	l.inner.Info(msg, attrs...)
}

// LogWarnMsg forwards a warning message and structured attributes to slog.
func (l slogLogger) LogWarnMsg(msg string, attrs ...any) {
	l.inner.Warn(msg, attrs...)
}

// LogErrorMsg forwards an error message and structured attributes to slog.
func (l slogLogger) LogErrorMsg(msg string, attrs ...any) {
	l.inner.Error(msg, attrs...)
}
