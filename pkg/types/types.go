// Package types contains API DTOs (data transfer objects) used by the API
// service to receive requests and return responses to clients.
//
// These types are intentionally NOT Kubernetes CRD types. CRD types live in
// pkg/apis/llmsafespace/v1; this package converts to/from them at the
// service boundary. Types here use plain Go types (e.g. *time.Time, not
// *metav1.Time) so the JSON contract returned to clients is free of
// Kubernetes-isms (kind, apiVersion, metadata).
package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Common errors
var (
	ErrNotFound         = errors.New("resource not found")
	ErrPermissionDenied = errors.New("permission denied")
	ErrInvalidInput     = errors.New("invalid input")
	ErrAlreadyExists    = errors.New("resource already exists")
)

// contextKey is an unexported type for context keys defined in this package.
// Using a typed key avoids collisions with string keys from other packages.
type contextKey string

// ContextKeyUserID is the context key used to store the authenticated user ID.
// Both the auth middleware and service layer use this constant so the key is
// always in sync.
const ContextKeyUserID contextKey = "userID"

// Sandbox is the API transfer object for a sandbox resource. It is NOT a
// Kubernetes object — there is no TypeMeta or ObjectMeta embedding. The
// service layer converts a v1.Sandbox CRD into one of these for client
// responses.
type Sandbox struct {
	ID                string            `json:"id"`
	Namespace         string            `json:"namespace,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	CreationTimestamp time.Time         `json:"creationTimestamp,omitempty"`

	Spec   SandboxSpec   `json:"spec"`
	Status SandboxStatus `json:"status"`
}

// SandboxSpec defines the desired state of a Sandbox
type SandboxSpec struct {
	// Runtime environment (e.g., python:3.10)
	Runtime string `json:"runtime"`

	// Security level for the sandbox
	SecurityLevel string `json:"securityLevel,omitempty"`

	// Timeout in seconds for sandbox operations
	Timeout int `json:"timeout,omitempty"`

	// Resource requirements
	Resources *ResourceRequirements `json:"resources,omitempty"`

	// Network access configuration
	NetworkAccess *NetworkAccess `json:"networkAccess,omitempty"`

	// Filesystem configuration
	Filesystem *FilesystemConfig `json:"filesystem,omitempty"`

	// Storage configuration
	Storage *StorageConfig `json:"storage,omitempty"`

	// Security context
	SecurityContext *SecurityContext `json:"securityContext,omitempty"`

	// Reference to a SandboxProfile
	ProfileRef *ProfileReference `json:"profileRef,omitempty"`
}

// ResourceRequirements defines resource limits for a sandbox
type ResourceRequirements struct {
	// CPU resource limit
	CPU string `json:"cpu,omitempty"`

	// Memory resource limit
	Memory string `json:"memory,omitempty"`

	// Ephemeral storage limit
	EphemeralStorage string `json:"ephemeralStorage,omitempty"`

	// GPU resource limit
	GPU string `json:"gpu,omitempty"`
}

// NetworkAccess defines network access configuration
type NetworkAccess struct {
	// Egress rules
	Egress []EgressRule `json:"egress,omitempty"`

	// Allow ingress traffic to sandbox
	Ingress bool `json:"ingress,omitempty"`
}

// EgressRule defines an egress rule
type EgressRule struct {
	// Domain name for egress filtering
	Domain string `json:"domain"`

	// Ports allowed for this domain
	Ports []PortRule `json:"ports,omitempty"`
}

// PortRule defines a port rule
type PortRule struct {
	// Port number
	Port int `json:"port"`

	// Protocol (TCP or UDP)
	Protocol string `json:"protocol,omitempty"`
}

// FilesystemConfig defines filesystem configuration
type FilesystemConfig struct {
	// Mount root filesystem as read-only
	ReadOnlyRoot bool `json:"readOnlyRoot,omitempty"`

	// Paths that should be writable
	WritablePaths []string `json:"writablePaths,omitempty"`
}

// StorageConfig defines storage configuration
type StorageConfig struct {
	// Enable persistent storage
	Persistent bool `json:"persistent,omitempty"`

	// Size of persistent volume
	VolumeSize string `json:"volumeSize,omitempty"`
}

// SecurityContext defines security context
type SecurityContext struct {
	// User ID to run container processes
	RunAsUser int64 `json:"runAsUser,omitempty"`

	// Group ID to run container processes
	RunAsGroup int64 `json:"runAsGroup,omitempty"`

	// Seccomp profile
	SeccompProfile string `json:"seccompProfile,omitempty"`

	// AppArmor profile
	AppArmorProfile string `json:"appArmorProfile,omitempty"`

	// Allow privilege escalation
	AllowPrivilegeEscalation bool `json:"allowPrivilegeEscalation,omitempty"`
}

// ProfileReference defines a reference to a SandboxProfile
type ProfileReference struct {
	// Name of SandboxProfile to use
	Name string `json:"name"`

	// Namespace of SandboxProfile
	Namespace string `json:"namespace,omitempty"`
}

// SandboxStatus defines the observed state of a Sandbox
type SandboxStatus struct {
	// Current phase of the sandbox
	Phase string `json:"phase,omitempty"`

	// Conditions for the sandbox
	Conditions []SandboxCondition `json:"conditions,omitempty"`

	// Name of the pod running the sandbox
	PodName string `json:"podName,omitempty"`

	// Start time of the sandbox
	StartTime *time.Time `json:"startTime,omitempty"`

	// Resource usage
	Resources *ResourceStatus `json:"resources,omitempty"`

	// Pod status (from Kubernetes pod)
	PodStatus string `json:"podStatus,omitempty"`

	// Pod IP address
	PodIP string `json:"podIP,omitempty"`

	// Pod start time
	PodStartTime *time.Time `json:"podStartTime,omitempty"`

	// Node name where pod is running
	NodeName string `json:"nodeName,omitempty"`

	// Container statuses
	ContainerStatuses []ContainerStatus `json:"containerStatuses,omitempty"`

	// Network information
	NetworkInfo *NetworkInfo `json:"networkInfo,omitempty"`

	// Events related to this sandbox
	Events []Event `json:"events,omitempty"`
}

// ContainerStateValue represents the state of a container
type ContainerStateValue string

const (
	ContainerStateRunning    ContainerStateValue = "Running"
	ContainerStateTerminated ContainerStateValue = "Terminated"
	ContainerStateWaiting    ContainerStateValue = "Waiting"
	ContainerStateUnknown    ContainerStateValue = "Unknown"
)

// ContainerStatus represents the status of a container
type ContainerStatus struct {
	// Container name
	Name string `json:"name"`

	// Whether the container is ready
	Ready bool `json:"ready"`

	// Number of times the container has been restarted
	RestartCount int32 `json:"restartCount"`

	// Container state
	State ContainerStateValue `json:"state"`

	// Time when the container started
	StartedAt *time.Time `json:"startedAt,omitempty"`

	// Time when the container finished
	FinishedAt *time.Time `json:"finishedAt,omitempty"`

	// Exit code if terminated
	ExitCode int32 `json:"exitCode,omitempty"`

	// Reason for current state
	Reason string `json:"reason,omitempty"`

	// Message regarding current state
	Message string `json:"message,omitempty"`
}

// NetworkInfo represents network information for a sandbox
type NetworkInfo struct {
	// Pod IP address
	PodIP string `json:"podIP,omitempty"`

	// Host IP address
	HostIP string `json:"hostIP,omitempty"`

	// Whether ingress is allowed
	Ingress bool `json:"ingress"`

	// Allowed egress domains
	EgressDomains []string `json:"egressDomains,omitempty"`
}

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

// SandboxCondition defines a condition of a sandbox
type SandboxCondition struct {
	// Type of condition
	Type string `json:"type"`

	// Status of the condition (True, False, Unknown)
	Status string `json:"status"`

	// Reason for the condition
	Reason string `json:"reason,omitempty"`

	// Message regarding the condition
	Message string `json:"message,omitempty"`

	// Last transition time
	LastTransitionTime *time.Time `json:"lastTransitionTime,omitempty"`
}

// ResourceStatus defines resource usage
type ResourceStatus struct {
	// Current CPU usage
	CPUUsage string `json:"cpuUsage,omitempty"`

	// Current memory usage
	MemoryUsage string `json:"memoryUsage,omitempty"`

	// Current ephemeral storage usage
	EphemeralStorageUsage string `json:"ephemeralStorageUsage,omitempty"`
}

// SandboxMetadata represents metadata about a sandbox stored in the database
type SandboxMetadata struct {
	ID string `json:"id" db:"id"`

	UserID string `json:"userId" db:"user_id"`

	Runtime string `json:"runtime" db:"runtime"`

	CreatedAt time.Time `json:"createdAt" db:"created_at"`

	UpdatedAt time.Time `json:"updatedAt" db:"updated_at"`

	Status string `json:"status" db:"status"`

	Name string `json:"name,omitempty" db:"name"`

	Labels map[string]string `json:"labels,omitempty"`

	WorkspaceID string `json:"workspaceId,omitempty" db:"workspace_id"`
}

// PaginationMetadata represents pagination metadata
type PaginationMetadata struct {
	// Total number of items
	Total int `json:"total"`

	// Start index
	Start int `json:"start"`

	// End index
	End int `json:"end"`

	// Limit per page
	Limit int `json:"limit"`

	// Offset
	Offset int `json:"offset"`
}

// CreateSandboxRequest represents a request to create a sandbox
type CreateSandboxRequest struct {
	// Runtime environment (e.g., python:3.10)
	Runtime string `json:"runtime"`

	// Security level for the sandbox
	SecurityLevel string `json:"securityLevel,omitempty"`

	// Timeout in seconds for sandbox operations
	Timeout int `json:"timeout,omitempty"`

	// User ID
	UserID string `json:"userId"`

	// Resource requirements
	Resources *ResourceRequirements `json:"resources,omitempty"`

	// Network access configuration
	NetworkAccess *NetworkAccess `json:"networkAccess,omitempty"`

	// WorkspaceRef is an optional workspace ID to associate with the sandbox.
	// When empty, a workspace is automatically created with defaults.
	WorkspaceRef string `json:"workspaceRef,omitempty"`
}

// ExecuteRequest represents a request to execute code or a command
type ExecuteRequest struct {
	// Sandbox ID
	SandboxID string `json:"sandboxId"`

	// Type of execution (code or command)
	Type string `json:"type"`

	// Content to execute
	Content string `json:"content"`

	// Timeout in seconds
	Timeout int `json:"timeout,omitempty"`

	// Environment variables
	Env map[string]string `json:"env,omitempty"`

	// Working directory
	WorkingDir string `json:"workingDir,omitempty"`
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

// InstallPackagesRequest represents a request to install packages
type InstallPackagesRequest struct {
	// Sandbox ID
	SandboxID string `json:"sandboxId"`

	// Packages to install
	Packages []string `json:"packages"`

	// Package manager to use
	PackageManager string `json:"packageManager,omitempty"`
}

// WSConnection represents a WebSocket connection
type WSConnection interface {
	// ReadMessage reads a message from the connection
	ReadMessage() (messageType int, p []byte, err error)

	// WriteMessage writes a message to the connection
	WriteMessage(messageType int, data []byte) error

	// Close closes the connection
	Close() error
}

// Session represents a WebSocket session
type Session struct {
	// Session ID
	ID string

	// User ID
	UserID string

	// Sandbox ID
	SandboxID string

	// WebSocket connection
	Conn WSConnection

	// Creation time
	CreatedAt time.Time
}

// User represents a user
type User struct {
	ID           string    `json:"id" db:"id"`
	Username     string    `json:"username" db:"username"`
	Email        string    `json:"email" db:"email"`
	PasswordHash string    `json:"-" db:"password_hash"`
	CreatedAt    time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt    time.Time `json:"updatedAt" db:"updated_at"`
	Active       bool      `json:"active" db:"active"`
	Role         string    `json:"role" db:"role"`
}

// RegisterRequest is the request body for user registration.
type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=64"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8,max=128"`
}

// LoginRequest is the request body for user login.
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// AuthResponse is returned after successful registration or login.
type AuthResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

// CreateAPIKeyRequest is the request body for creating an API key.
type CreateAPIKeyRequest struct {
	Name string `json:"name" binding:"required,min=1,max=128"`
}

// APIKey represents an API key record returned in list responses.
type APIKey struct {
	ID        string     `json:"id"`
	UserID    string     `json:"-" db:"user_id"`
	Name      string     `json:"name"`
	Key       string     `json:"key,omitempty"`
	Prefix    string     `json:"prefix"`
	Active    bool       `json:"active"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// SandboxListResult is the typed return value for ListSandboxes. It bundles
// live Kubernetes status with database metadata and pagination so callers never
// receive untyped maps.
type SandboxListResult struct {
	Items      []SandboxListItem   `json:"items"`
	Pagination *PaginationMetadata `json:"pagination,omitempty"`
}

// SandboxListItem merges database metadata with live Kubernetes status.
type SandboxListItem struct {
	// Database fields
	ID        string            `json:"id"`
	UserID    string            `json:"userId"`
	Runtime   string            `json:"runtime"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
	Status    string            `json:"status"`
	Name      string            `json:"name,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`

	// Live Kubernetes status (best-effort; zero values when unavailable)
	Phase       string     `json:"phase,omitempty"`
	StartTime   *time.Time `json:"startTime,omitempty"`
	CPUUsage    string     `json:"cpuUsage,omitempty"`
	MemoryUsage string     `json:"memoryUsage,omitempty"`
}

// UserUpdates carries the fields that may be changed on a User record.
// All fields are pointers — nil means "do not update this field".
type UserUpdates struct {
	Username *string `json:"username,omitempty"`
	Email    *string `json:"email,omitempty"`
	Active   *bool   `json:"active,omitempty"`
	Role     *string `json:"role,omitempty"`
}

// SandboxUpdates carries the fields that may be changed on a SandboxMetadata record.
// All scalar fields are pointers — nil means "do not update this field".
// Labels nil means "do not touch labels"; non-nil replaces the label set entirely.
type SandboxUpdates struct {
	Status *string           `json:"status,omitempty"`
	Name   *string           `json:"name,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

// CachedSession is the typed representation of a WebSocket session stored in
// the cache. It replaces the previous map[string]interface{} bag.
type CachedSession struct {
	SessionID string `json:"sessionId"`
	UserID    string `json:"userId"`
	SandboxID string `json:"sandboxId"`
}

type Message struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type SandboxNotFoundError struct {
	ID string
}

func (e *SandboxNotFoundError) Error() string {
	return fmt.Sprintf("sandbox %s not found", e.ID)
}

// Workspace is the API transfer object for a workspace resource.
type Workspace struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	UserID      string            `json:"userId"`
	Runtime     string            `json:"runtime"`
	StorageSize string            `json:"storageSize"`
	Phase       string            `json:"phase"`
	PVCName     string            `json:"pvcName,omitempty"`
	SandboxID   string            `json:"sandboxId,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

// CreateWorkspaceRequest is the request body for creating a workspace.
type CreateWorkspaceRequest struct {
	Name         string            `json:"name"`
	Runtime      string            `json:"runtime"`
	StorageSize  string            `json:"storageSize"`
	StorageClass string            `json:"storageClass,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
}

// ListOptions carries pagination and filtering parameters.
type ListOptions struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// WorkspaceListResult bundles workspace list items with pagination.
type WorkspaceListResult struct {
	Items      []WorkspaceListItem `json:"items"`
	Pagination *PaginationMetadata `json:"pagination,omitempty"`
}

// WorkspaceListItem is a lightweight workspace representation for list responses.
type WorkspaceListItem struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	UserID            string    `json:"userId"`
	Runtime           string    `json:"runtime"`
	StorageSize       string    `json:"storageSize"`
	Phase             string    `json:"phase,omitempty"`
	MaxActiveSessions int       `json:"maxActiveSessions,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// WorkspaceStatusResult carries the status fields read from the Workspace CRD.
type WorkspaceStatusResult struct {
	Phase          string                     `json:"phase"`
	PVCName        string                     `json:"pvcName,omitempty"`
	ActiveSessions int                        `json:"activeSessions"`
	LastActivityAt *time.Time                 `json:"lastActivityAt,omitempty"`
	Message        string                     `json:"message,omitempty"`
	Conditions     []WorkspaceConditionResult `json:"conditions,omitempty"`
}

// WorkspaceConditionResult carries a single workspace condition.
type WorkspaceConditionResult struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// SetCredentialsRequest is the request body for setting workspace credentials.
type SetCredentialsRequest struct {
	Provider string          `json:"provider"`
	Config   json.RawMessage `json:"config"`
}

// WorkspaceMetadata is the database record for a workspace.
type WorkspaceMetadata struct {
	ID          string    `json:"id" db:"id"`
	UserID      string    `json:"userId" db:"user_id"`
	Name        string    `json:"name" db:"name"`
	Runtime     string    `json:"runtime" db:"runtime"`
	StorageSize string    `json:"storageSize" db:"storage_size"`
	CreatedAt   time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt   time.Time `json:"updatedAt" db:"updated_at"`
}

// WorkspaceUpdates carries the fields that may be changed on a WorkspaceMetadata record.
type WorkspaceUpdates struct {
	Name *string `json:"name,omitempty"`
}

// WorkspaceNotFoundError is returned when a workspace cannot be found.
type WorkspaceNotFoundError struct {
	ID string
}

func (e *WorkspaceNotFoundError) Error() string {
	return fmt.Sprintf("workspace %s not found", e.ID)
}

// --- Frontend types (Phase A) ---

// AuthConfig is returned by GET /auth/config for feature-flag discovery.
type AuthConfig struct {
	RegistrationEnabled bool     `json:"registrationEnabled"`
	OIDCEnabled         bool     `json:"oidcEnabled"`
	SSOProviders        []string `json:"ssoProviders,omitempty"`
}

// ActivateWorkspaceResponse is returned by POST /workspaces/:id/activate.
type ActivateWorkspaceResponse struct {
	Resumed   string `json:"resumed"`
	Suspended string `json:"suspended,omitempty"`
}

// EnsureSessionResponse is returned by POST /workspaces/:id/sessions/new.
// It guarantees the workspace is active with a running pod, returning the
// workspace ID and session ID for immediate use.
type EnsureSessionResponse struct {
	WorkspaceID    string `json:"workspaceId"`
	WorkspacePhase string `json:"workspacePhase"`
	SessionID      string `json:"sessionId"`
	Resumed        bool   `json:"resumed"`
}

// SessionListItem is sidebar metadata for a session (NOT message bodies).
type SessionListItem struct {
	ID            string     `json:"id"`
	Title         string     `json:"title,omitempty"`
	LastMessageAt *time.Time `json:"lastMessageAt,omitempty"`
	MessageCount  int        `json:"messageCount"`
	Status        string     `json:"status"` // "active" | "idle"
}

// ActiveSessionsResponse is returned by GET /workspaces/:id/sessions/active.
type ActiveSessionsResponse struct {
	Active    []string `json:"active"`
	MaxActive int      `json:"maxActive"`
}
