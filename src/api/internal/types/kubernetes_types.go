package types

import (
	"time"
)

// ExecutionRequest defines a request to execute code or a command
type ExecutionRequest struct {
	Type    string `json:"type"`    // "code" or "command"
	Content string `json:"content"` // Code or command to execute
	Timeout int    `json:"timeout"` // Execution timeout in seconds
	Stream  bool   `json:"stream"`  // Whether to stream the output
}

// ExecutionResult defines the result of code or command execution
type ExecutionResult struct {
	ID          string    `json:"id"`
	Status      string    `json:"status"`
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt"`
	ExitCode    int       `json:"exitCode"`
	Stdout      string    `json:"stdout"`
	Stderr      string    `json:"stderr"`
}

// FileRequest represents a file operation request
type FileRequest struct {
	Path    string  // Path to the file
	Content []byte  // Content for upload operations
	IsDir   bool    // Whether this is a directory operation
}

// FileResult represents the result of a file operation
type FileResult struct {
	Path      string    // Path to the file
	Size      int64     // Size of the file in bytes
	IsDir     bool      // Whether this is a directory
	CreatedAt time.Time // Creation time
	UpdatedAt time.Time // Last modification time
	Checksum  string    // Optional checksum of the file
}

// FileInfo represents information about a file
type FileInfo struct {
	Path      string    // Path to the file
	Size      int64     // Size of the file in bytes
	IsDir     bool      // Whether this is a directory
	CreatedAt time.Time // Creation time
	UpdatedAt time.Time // Last modification time
	Mode      uint32    // File mode/permissions
	Owner     string    // Owner of the file
	Group     string    // Group of the file
}

// FileList represents a list of files
type FileList struct {
	Files []FileInfo // List of files
	Path  string     // Path that was listed
	Total int        // Total number of files
}
