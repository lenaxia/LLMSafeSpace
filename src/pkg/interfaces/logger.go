package interfaces

// LoggerInterface defines the interface for logging operations
type LoggerInterface interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Warn(msg string, keysAndValues ...interface{})
	Error(msg string, err error, keysAndValues ...interface{})
	Fatal(msg string, err error, keysAndValues ...interface{})
	With(keysAndValues ...interface{}) LoggerInterface
	Sync() error
}
