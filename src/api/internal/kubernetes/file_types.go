package kubernetes

import (
	"time"
)

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

// FileStat represents detailed file statistics
type FileStat struct {
	Path       string    // Path to the file
	Size       int64     // Size of the file in bytes
	IsDir      bool      // Whether this is a directory
	Mode       uint32    // File mode/permissions
	ModTime    time.Time // Last modification time
	AccessTime time.Time // Last access time
	ChangeTime time.Time // Last status change time
	Owner      string    // Owner of the file
	Group      string    // Group of the file
	Device     uint64    // Device ID
	Inode      uint64    // Inode number
	Links      uint64    // Number of hard links
	BlockSize  int64     // Block size
	Blocks     int64     // Number of blocks
}

// DirectoryCreateRequest represents a request to create a directory
type DirectoryCreateRequest struct {
	Path       string // Path to create
	Recursive  bool   // Whether to create parent directories
	Permission uint32 // Permission mode
}

// FileSystemInfo represents information about the filesystem
type FileSystemInfo struct {
	TotalSpace      int64  // Total space in bytes
	AvailableSpace  int64  // Available space in bytes
	UsedSpace       int64  // Used space in bytes
	FileSystemType  string // Type of filesystem
	MountPoint      string // Mount point
	InodeTotal      int64  // Total inodes
	InodeAvailable  int64  // Available inodes
	InodeUsed       int64  // Used inodes
	ReadOnly        bool   // Whether the filesystem is read-only
	WorkspaceQuota  int64  // Quota for the workspace
	WorkspaceUsage  int64  // Current usage of the workspace
}
