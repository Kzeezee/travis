package log

import (
	"io"
	"log/slog"

	"github.com/Kzeezee/travis/internal/core"
)

// Level names the minimum severity a built logger should record.
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

type slogLogger struct {
	inner *slog.Logger
}

// BuildDefaultLogger returns the process default logger behind the Travis facade.
func BuildDefaultLogger() core.Logger {
	return slogLogger{inner: slog.Default()}
}

// BuildTextLogger returns a logger for CLI-visible text output at the requested level.
func BuildTextLogger(out io.Writer, level Level) core.Logger {
	if out == nil {
		out = io.Discard
	}
	logger := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: selectSlogLevel(level)}))
	return slogLogger{inner: logger}
}

// BuildNopLogger returns a logger for tests or call sites that intentionally suppress logs.
func BuildNopLogger() core.Logger {
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
