package kubernetes

import (
	"time"
)

// FileRequest represents a file operation request
type FileRequest struct {
	Path    string
	Content []byte
}

// FileResult represents the result of a file operation
type FileResult struct {
	Path      string
	Size      int64
	IsDir     bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// FileInfo represents information about a file
type FileInfo struct {
	Path      string
	Size      int64
	IsDir     bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// FileList represents a list of files
type FileList struct {
	Files []FileInfo
}
