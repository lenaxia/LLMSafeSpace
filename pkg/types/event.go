// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package types

import "time"

// Event represents a Kubernetes event
type Event struct {
	// Event type (Normal, Warning)
	Type string `json:"type"`

	// Event reason
	Reason string `json:"reason"`

	// Event message
	Message string `json:"message"`

	// Event count
	Count int32 `json:"count"`

	// Event time
	Time *time.Time `json:"time,omitempty"`

	// Event source (Pod, Sandbox, etc.)
	Source string `json:"source,omitempty"`
}

// ResourceStatus defines resource usage
type ResourceStatus struct {
	// Current CPU usage
	CPUUsage string `json:"cpuUsage,omitempty"`

	// Current memory usage
	MemoryUsage string `json:"memoryUsage,omitempty"`
}

// ExecutionResult represents the result of an execution
type ExecutionResult struct {
	// Stdout output
	Stdout string `json:"stdout"`

	// Stderr output
	Stderr string `json:"stderr"`

	// Exit code
	ExitCode int `json:"exitCode"`

	// Execution time in milliseconds
	ExecutionTime int64 `json:"executionTime"`

	// Error message if any
	Error string `json:"error,omitempty"`
}

// FileInfo represents information about a file
type FileInfo struct {
	// File name
	Name string `json:"name"`

	// File path
	Path string `json:"path"`

	// File size in bytes
	Size int64 `json:"size"`

	// File mode
	Mode string `json:"mode"`

	// Last modified time
	ModTime time.Time `json:"modTime"`

	// Whether it's a directory
	IsDir bool `json:"isDir"`
}
