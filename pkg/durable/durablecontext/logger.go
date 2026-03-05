package durablecontext

import (
	"fmt"
	"log"
	"sync"

	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/state"
)

// DurableLogger provides replay-aware logging capabilities.
type DurableLogger interface {
	Debug(msg string, args ...interface{})
	Info(msg string, args ...interface{})
	Warn(msg string, args ...interface{})
	Error(msg string, args ...interface{})
}

// LogLevel represents the logging level.
type LogLevel int

const (
	LogLevelDebug LogLevel = iota
	LogLevelInfo
	LogLevelWarn
	LogLevelError
)

// LogInfo contains information about a log entry.
type LogInfo struct {
	OperationID   string
	OperationName string
	Message       string
	Level         LogLevel
	Args          []interface{}
}

// durableLogger is a replay-aware logger implementation.
type durableLogger struct {
	operationID   string
	operationName string
	state         *state.ExecutionState
	mu            sync.Mutex
	seenLogs      map[string]bool
}

// NewDurableLogger creates a new durable logger.
func NewDurableLogger(operationID, operationName string, st *state.ExecutionState) DurableLogger {
	return &durableLogger{
		operationID:   operationID,
		operationName: operationName,
		state:         st,
		seenLogs:      make(map[string]bool),
	}
}

// logKey creates a unique key for deduplication.
func (l *durableLogger) logKey(level LogLevel, msg string) string {
	return fmt.Sprintf("%s:%s:%d:%s", l.operationID, l.operationName, level, msg)
}

// shouldLog checks if this log should be emitted (deduplication).
func (l *durableLogger) shouldLog(level LogLevel, msg string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	key := l.logKey(level, msg)
	if l.seenLogs[key] {
		return false
	}
	l.seenLogs[key] = true
	return true
}

// log performs the actual logging.
func (l *durableLogger) log(level LogLevel, msg string, args ...interface{}) {
	if !l.shouldLog(level, msg) {
		return
	}

	levelStr := ""
	switch level {
	case LogLevelDebug:
		levelStr = "DEBUG"
	case LogLevelInfo:
		levelStr = "INFO"
	case LogLevelWarn:
		levelStr = "WARN"
	case LogLevelError:
		levelStr = "ERROR"
	}

	prefix := fmt.Sprintf("[%s] [%s] ", levelStr, l.operationName)
	if len(args) > 0 {
		log.Printf(prefix+msg, args...)
	} else {
		log.Print(prefix + msg)
	}
}

func (l *durableLogger) Debug(msg string, args ...interface{}) {
	l.log(LogLevelDebug, msg, args...)
}

func (l *durableLogger) Info(msg string, args ...interface{}) {
	l.log(LogLevelInfo, msg, args...)
}

func (l *durableLogger) Warn(msg string, args ...interface{}) {
	l.log(LogLevelWarn, msg, args...)
}

func (l *durableLogger) Error(msg string, args ...interface{}) {
	l.log(LogLevelError, msg, args...)
}

// simpleLogger is a basic logger for non-durable operations.
type simpleLogger struct{}

// NewSimpleLogger creates a simple logger.
func NewSimpleLogger() DurableLogger {
	return &simpleLogger{}
}

func (s *simpleLogger) Debug(msg string, args ...interface{}) {
	if len(args) > 0 {
		log.Printf("[DEBUG] "+msg, args...)
	} else {
		log.Print("[DEBUG] " + msg)
	}
}

func (s *simpleLogger) Info(msg string, args ...interface{}) {
	if len(args) > 0 {
		log.Printf("[INFO] "+msg, args...)
	} else {
		log.Print("[INFO] " + msg)
	}
}

func (s *simpleLogger) Warn(msg string, args ...interface{}) {
	if len(args) > 0 {
		log.Printf("[WARN] "+msg, args...)
	} else {
		log.Print("[WARN] " + msg)
	}
}

func (s *simpleLogger) Error(msg string, args ...interface{}) {
	if len(args) > 0 {
		log.Printf("[ERROR] "+msg, args...)
	} else {
		log.Print("[ERROR] " + msg)
	}
}
