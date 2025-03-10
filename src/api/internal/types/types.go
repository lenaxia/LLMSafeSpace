package types

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"github.com/gorilla/websocket"

	llmsafespacev1 "github.com/lenaxia/llmsafespace/api/internal/kubernetes/apis/llmsafespace/v1"
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

// CreateSandboxRequest defines the request for creating a sandbox
type CreateSandboxRequest struct {
	Runtime       string                        `json:"runtime"`
	SecurityLevel string                        `json:"securityLevel,omitempty"`
	Timeout       int                           `json:"timeout,omitempty"`
	Resources     *llmsafespacev1.ResourceRequirements `json:"resources,omitempty"`
	NetworkAccess *llmsafespacev1.NetworkAccess        `json:"networkAccess,omitempty"`
	UseWarmPool   bool                          `json:"useWarmPool,omitempty"`
	UserID        string                        `json:"-"`
	Namespace     string                        `json:"-"`
}

// ExecuteRequest defines the request for executing code or a command
type ExecuteRequest struct {
	Type      string `json:"type"`      // "code" or "command"
	Content   string `json:"content"`   // Code or command to execute
	Timeout   int    `json:"timeout"`   // Execution timeout in seconds
	SandboxID string `json:"-"`         // Set by the handler
}

// InstallPackagesRequest defines the request for installing packages
type InstallPackagesRequest struct {
	Packages  []string `json:"packages"` // Packages to install
	Manager   string   `json:"manager"`  // Package manager to use
	SandboxID string   `json:"-"`        // Set by the handler
}

// Sandbox represents a sandbox instance
type Sandbox struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   llmsafespacev1.SandboxSpec   `json:"spec,omitempty"`
	Status llmsafespacev1.SandboxStatus `json:"status,omitempty"`
}

// SandboxStatus represents the status of a sandbox
type SandboxStatus = llmsafespacev1.SandboxStatus

// WarmPool represents a warm pool of sandbox instances
type WarmPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   llmsafespacev1.WarmPoolSpec   `json:"spec,omitempty"`
	Status llmsafespacev1.WarmPoolStatus `json:"status,omitempty"`
}

// CreateWarmPoolRequest defines the request for creating a warm pool
type CreateWarmPoolRequest struct {
	Name            string                                `json:"name"`
	Runtime         string                                `json:"runtime"`
	MinSize         int                                   `json:"minSize"`
	MaxSize         int                                   `json:"maxSize,omitempty"`
	SecurityLevel   string                                `json:"securityLevel,omitempty"`
	TTL             int                                   `json:"ttl,omitempty"`
	Resources       *llmsafespacev1.ResourceRequirements  `json:"resources,omitempty"`
	ProfileRef      *llmsafespacev1.ProfileReference      `json:"profileRef,omitempty"`
	PreloadPackages []string                              `json:"preloadPackages,omitempty"`
	PreloadScripts  []llmsafespacev1.PreloadScript        `json:"preloadScripts,omitempty"`
	AutoScaling     *llmsafespacev1.AutoScalingConfig     `json:"autoScaling,omitempty"`
	UserID          string                                `json:"-"`
	Namespace       string                                `json:"-"`
}

// UpdateWarmPoolRequest defines the request for updating a warm pool
type UpdateWarmPoolRequest struct {
	Name        string                            `json:"name"`
	MinSize     int                               `json:"minSize,omitempty"`
	MaxSize     int                               `json:"maxSize,omitempty"`
	TTL         int                               `json:"ttl,omitempty"`
	AutoScaling *llmsafespacev1.AutoScalingConfig `json:"autoScaling,omitempty"`
	UserID      string                            `json:"-"`
	Namespace   string                            `json:"-"`
}

// Session represents a WebSocket session
type Session struct {
	ID              string
	UserID          string
	SandboxID       string
	Conn            *websocket.Conn
	CancellationFns map[string]context.CancelFunc
}
