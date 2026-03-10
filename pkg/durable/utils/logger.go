package utils

import (
	"log/slog"
	"os"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

// DefaultLogger is the built-in structured logger backed by log/slog.
// It emits JSON to stdout and implements the types.Logger interface.
type DefaultLogger struct {
	inner *slog.Logger
}

// NewDefaultLogger creates a DefaultLogger with a JSON handler on stdout,
// pre-populated with the given execution context fields.
func NewDefaultLogger(executionArn, requestID, tenantID string) *DefaultLogger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	base := slog.New(handler).With(
		"executionArn", executionArn,
		"requestId", requestID,
		"tenantId", tenantID,
	)
	return &DefaultLogger{inner: base}
}

// NewDefaultLoggerFromSlog creates a DefaultLogger backed by an existing *slog.Logger,
// allowing callers to configure their own handler, level, or output destination.
func NewDefaultLoggerFromSlog(logger *slog.Logger) *DefaultLogger {
	return &DefaultLogger{inner: logger}
}

// WithOperationID returns a copy of the logger with the given operation ID set.
func (l *DefaultLogger) WithOperationID(opID string) *DefaultLogger {
	return &DefaultLogger{inner: l.inner.With("operationId", opID)}
}

// Info logs at INFO level.
func (l *DefaultLogger) Info(message string, fields ...any) {
	l.inner.Info(message, fields...)
}

// Warn logs at WARN level.
func (l *DefaultLogger) Warn(message string, fields ...any) {
	l.inner.Warn(message, fields...)
}

// Error logs at ERROR level.
func (l *DefaultLogger) Error(message string, err error, fields ...any) {
	args := append([]any{"error", err}, fields...)
	l.inner.Error(message, args...)
}

// Debug logs at DEBUG level.
func (l *DefaultLogger) Debug(message string, fields ...any) {
	l.inner.Debug(message, fields...)
}

// ModeAwareLogger wraps a Logger to suppress output during replay mode.
type ModeAwareLogger struct {
	inner    types.Logger
	getModeF func() bool // returns true if should log
}

// NewModeAwareLogger creates a logger that only emits during execution (not replay) mode.
func NewModeAwareLogger(inner types.Logger, shouldLog func() bool) *ModeAwareLogger {
	return &ModeAwareLogger{inner: inner, getModeF: shouldLog}
}

func (m *ModeAwareLogger) Info(message string, fields ...any) {
	if m.getModeF() {
		m.inner.Info(message, fields...)
	}
}

func (m *ModeAwareLogger) Warn(message string, fields ...any) {
	if m.getModeF() {
		m.inner.Warn(message, fields...)
	}
}

func (m *ModeAwareLogger) Error(message string, err error, fields ...any) {
	if m.getModeF() {
		m.inner.Error(message, err, fields...)
	}
}

func (m *ModeAwareLogger) Debug(message string, fields ...any) {
	if m.getModeF() {
		m.inner.Debug(message, fields...)
	}
}

// NopLogger is a logger that discards all output.
type NopLogger struct{}

func (NopLogger) Info(message string, fields ...any)             {}
func (NopLogger) Warn(message string, fields ...any)             {}
func (NopLogger) Error(message string, err error, fields ...any) {}
func (NopLogger) Debug(message string, fields ...any)            {}
