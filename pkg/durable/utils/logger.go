package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

// DefaultLogger is the built-in structured logger that emits JSON to stdout.
// It implements the types.Logger interface.
type DefaultLogger struct {
	executionArn string
	requestID    string
	tenantID     string
	operationID  string
}

// NewDefaultLogger creates a new DefaultLogger with execution context.
func NewDefaultLogger(executionArn, requestID, tenantID string) *DefaultLogger {
	return &DefaultLogger{
		executionArn: executionArn,
		requestID:    requestID,
		tenantID:     tenantID,
	}
}

// WithOperationID returns a copy of the logger with the given operation ID set.
func (l *DefaultLogger) WithOperationID(opID string) *DefaultLogger {
	copy := *l
	copy.operationID = opID
	return &copy
}

type logEntry struct {
	Timestamp    string `json:"timestamp"`
	Level        string `json:"level"`
	ExecutionArn string `json:"executionArn,omitempty"`
	RequestID    string `json:"requestId,omitempty"`
	TenantID     string `json:"tenantId,omitempty"`
	OperationID  string `json:"operationId,omitempty"`
	Message      any    `json:"message"`
	Error        string `json:"error,omitempty"`
}

func (l *DefaultLogger) emit(level string, message any, err error) {
	entry := logEntry{
		Timestamp:    time.Now().UTC().Format(time.RFC3339Nano),
		Level:        level,
		ExecutionArn: l.executionArn,
		RequestID:    l.requestID,
		TenantID:     l.tenantID,
		OperationID:  l.operationID,
		Message:      message,
	}
	if err != nil {
		entry.Error = err.Error()
	}

	b, jsonErr := json.Marshal(entry)
	if jsonErr != nil {
		fmt.Fprintf(os.Stdout, `{"level":"%s","message":%q,"jsonError":%q}`+"\n", level, fmt.Sprintf("%v", message), jsonErr.Error())
		return
	}
	fmt.Fprintf(os.Stdout, "%s\n", b)
}

func fieldsToMap(fields []any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	m := make(map[string]any, len(fields)/2+1)
	for i := 0; i+1 < len(fields); i += 2 {
		key := fmt.Sprintf("%v", fields[i])
		m[key] = fields[i+1]
	}
	if len(fields)%2 != 0 {
		m["_extra"] = fields[len(fields)-1]
	}
	return m
}

// Info logs at INFO level.
func (l *DefaultLogger) Info(message string, fields ...any) {
	msg := any(message)
	if len(fields) > 0 {
		m := fieldsToMap(fields)
		m["message"] = message
		msg = m
	}
	l.emit("INFO", msg, nil)
}

// Warn logs at WARN level.
func (l *DefaultLogger) Warn(message string, fields ...any) {
	msg := any(message)
	if len(fields) > 0 {
		m := fieldsToMap(fields)
		m["message"] = message
		msg = m
	}
	l.emit("WARN", msg, nil)
}

// Error logs at ERROR level.
func (l *DefaultLogger) Error(message string, err error, fields ...any) {
	msg := any(message)
	if len(fields) > 0 {
		m := fieldsToMap(fields)
		m["message"] = message
		msg = m
	}
	l.emit("ERROR", msg, err)
}

// Debug logs at DEBUG level.
func (l *DefaultLogger) Debug(message string, fields ...any) {
	msg := any(message)
	if len(fields) > 0 {
		m := fieldsToMap(fields)
		m["message"] = message
		msg = m
	}
	l.emit("DEBUG", msg, nil)
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
