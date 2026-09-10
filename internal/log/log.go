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

type slogLogger struct {
	inner *slog.Logger
}

// BuildDefaultLogger returns the process default logger behind the Travis facade.
func BuildDefaultLogger() Logger {
	return slogLogger{inner: slog.Default()}
}

// BuildTextLogger returns a logger for CLI-visible text output at the requested level.
func BuildTextLogger(out io.Writer, level Level) Logger {
	if out == nil {
		out = io.Discard
	}
	logger := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: selectSlogLevel(level)}))
	return slogLogger{inner: logger}
}

// BuildNopLogger returns a logger for tests or call sites that intentionally suppress logs.
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

func (l slogLogger) LogDebugMsg(msg string, attrs ...any) {
	l.inner.Debug(msg, attrs...)
}

func (l slogLogger) LogInfoMsg(msg string, attrs ...any) {
	l.inner.Info(msg, attrs...)
}

func (l slogLogger) LogWarnMsg(msg string, attrs ...any) {
	l.inner.Warn(msg, attrs...)
}

func (l slogLogger) LogErrorMsg(msg string, attrs ...any) {
	l.inner.Error(msg, attrs...)
}
