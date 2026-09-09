package log

import (
	"io"
	"log/slog"
)

type Logger interface {
	Debug(msg string, attrs ...any)
	Info(msg string, attrs ...any)
	Warn(msg string, attrs ...any)
	Error(msg string, attrs ...any)
}

type slogLogger struct {
	inner *slog.Logger
}

func New(logger *slog.Logger) Logger {
	if logger == nil {
		logger = slog.Default()
	}
	return slogLogger{inner: logger}
}

func Nop() Logger {
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func (l slogLogger) Debug(msg string, attrs ...any) {
	l.inner.Debug(msg, attrs...)
}

func (l slogLogger) Info(msg string, attrs ...any) {
	l.inner.Info(msg, attrs...)
}

func (l slogLogger) Warn(msg string, attrs ...any) {
	l.inner.Warn(msg, attrs...)
}

func (l slogLogger) Error(msg string, attrs ...any) {
	l.inner.Error(msg, attrs...)
}
