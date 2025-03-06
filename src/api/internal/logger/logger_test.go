package logger

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestNewLogger(t *testing.T) {
	// Test creating a development logger
	devLogger, err := New(true, "debug", "console")
	if err != nil {
		t.Fatalf("Failed to create development logger: %v", err)
	}
	if devLogger == nil {
		t.Fatal("Expected non-nil development logger")
	}

	// Test creating a production logger
	prodLogger, err := New(false, "info", "json")
	if err != nil {
		t.Fatalf("Failed to create production logger: %v", err)
	}
	if prodLogger == nil {
		t.Fatal("Expected non-nil production logger")
	}

	// Test invalid log level (should default to info)
	invalidLevelLogger, err := New(false, "invalid", "json")
	if err != nil {
		t.Fatalf("Failed to create logger with invalid level: %v", err)
	}
	if invalidLevelLogger == nil {
		t.Fatal("Expected non-nil logger with invalid level")
	}
}

func TestLoggerOutput(t *testing.T) {
	// Create a logger with an in-memory observer
	core, recorded := observer.New(zapcore.DebugLevel)
	testLogger := &Logger{
		logger: zap.New(core),
	}

	// Test different log levels
	testLogger.Debug("debug message", "key1", "value1")
	testLogger.Info("info message", "key2", "value2")
	testLogger.Warn("warn message", "key3", "value3")
	testLogger.Error("error message", nil, "key4", "value4")

	// Verify log entries
	logs := recorded.All()
	if len(logs) != 4 {
		t.Fatalf("Expected 4 log entries, got %d", len(logs))
	}

	// Check debug entry
	if logs[0].Level != zapcore.DebugLevel || logs[0].Message != "debug message" {
		t.Errorf("Unexpected debug log entry: %v", logs[0])
	}
	if logs[0].Context[0].Key != "key1" || logs[0].Context[0].String != "value1" {
		t.Errorf("Unexpected debug log context: %v", logs[0].Context)
	}

	// Check info entry
	if logs[1].Level != zapcore.InfoLevel || logs[1].Message != "info message" {
		t.Errorf("Unexpected info log entry: %v", logs[1])
	}

	// Check warn entry
	if logs[2].Level != zapcore.WarnLevel || logs[2].Message != "warn message" {
		t.Errorf("Unexpected warn log entry: %v", logs[2])
	}

	// Check error entry
	if logs[3].Level != zapcore.ErrorLevel || logs[3].Message != "error message" {
		t.Errorf("Unexpected error log entry: %v", logs[3])
	}
}

func TestLoggerWith(t *testing.T) {
	// Create a logger with an in-memory observer
	core, recorded := observer.New(zapcore.DebugLevel)
	testLogger := &Logger{
		logger: zap.New(core),
	}

	// Create a logger with additional fields
	contextLogger := testLogger.With("context_key", "context_value")
	contextLogger.Info("context message")

	// Verify log entry has the context field
	logs := recorded.All()
	if len(logs) != 1 {
		t.Fatalf("Expected 1 log entry, got %d", len(logs))
	}

	found := false
	for _, field := range logs[0].Context {
		if field.Key == "context_key" && field.String == "context_value" {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Context field not found in log entry: %v", logs[0].Context)
	}
}
