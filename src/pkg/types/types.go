package types

import (
	"errors"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Common errors
var (
	ErrNotFound         = errors.New("resource not found")
	ErrPermissionDenied = errors.New("permission denied")
	ErrInvalidInput     = errors.New("invalid input")
	ErrAlreadyExists    = errors.New("resource already exists")
)

// Sandbox represents a sandbox environment
type Sandbox struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SandboxSpec   `json:"spec,omitempty"`
	Status SandboxStatus `json:"status,omitempty"`
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
	StartTime *metav1.Time `json:"startTime,omitempty"`
	
	// Resource usage
	Resources *ResourceStatus `json:"resources,omitempty"`
	
	// Reference to warm pod
	WarmPodRef *WarmPodReference `json:"warmPodRef,omitempty"`
	
	// Pod status (from Kubernetes pod)
	PodStatus string `json:"podStatus,omitempty"`
	
	// Pod IP address
	PodIP string `json:"podIP,omitempty"`
	
	// Pod start time
	PodStartTime *metav1.Time `json:"podStartTime,omitempty"`
	
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
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	
	// Time when the container finished
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`
	
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
	Time *metav1.Time `json:"time,omitempty"`
	
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
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`
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

// WarmPodReference defines a reference to a WarmPod
type WarmPodReference struct {
	// Name of the WarmPod
	Name string `json:"name"`
	
	// Namespace of the WarmPod
	Namespace string `json:"namespace,omitempty"`
}

// SandboxList contains a list of Sandbox
type SandboxList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Sandbox `json:"items"`
}

// SandboxMetadata represents metadata about a sandbox stored in the database
type SandboxMetadata struct {
	// Sandbox ID
	ID string `json:"id" db:"id"`
	
	// User ID
	UserID string `json:"userId" db:"user_id"`
	
	// Runtime environment
	Runtime string `json:"runtime" db:"runtime"`
	
	// Creation time
	CreatedAt time.Time `json:"createdAt" db:"created_at"`
	
	// Last update time
	UpdatedAt time.Time `json:"updatedAt" db:"updated_at"`
	
	// Current status
	Status string `json:"status" db:"status"`
	
	// Optional name
	Name string `json:"name,omitempty" db:"name"`
	
	// Labels
	Labels map[string]string `json:"labels,omitempty"`
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
	
	// Use warm pool
	UseWarmPool bool `json:"useWarmPool,omitempty"`
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
	// User ID
	ID string `json:"id" db:"id"`
	
	// Username
	Username string `json:"username" db:"username"`
	
	// Email
	Email string `json:"email" db:"email"`
	
	// Creation time
	CreatedAt time.Time `json:"createdAt" db:"created_at"`
}

// WarmPod represents a warm pod
type WarmPod struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WarmPodSpec   `json:"spec,omitempty"`
	Status WarmPodStatus `json:"status,omitempty"`
}

// WarmPodSpec defines the desired state of a WarmPod
type WarmPodSpec struct {
	// Runtime environment
	Runtime string `json:"runtime"`
	
	// Security level
	SecurityLevel string `json:"securityLevel,omitempty"`
	
	// Resource requirements
	Resources *ResourceRequirements `json:"resources,omitempty"`
}

// WarmPodStatus defines the observed state of a WarmPod
type WarmPodStatus struct {
	// Current phase of the warm pod
	Phase string `json:"phase,omitempty"`
	
	// Pod name
	PodName string `json:"podName,omitempty"`
	
	// Creation time
	CreationTime *metav1.Time `json:"creationTime,omitempty"`
}

// WarmPool represents a warm pool
type WarmPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WarmPoolSpec   `json:"spec,omitempty"`
	Status WarmPoolStatus `json:"status,omitempty"`
}

// WarmPoolSpec defines the desired state of a WarmPool
type WarmPoolSpec struct {
	// Runtime environment
	Runtime string `json:"runtime"`
	
	// Minimum number of warm pods
	MinPods int `json:"minPods"`
	
	// Maximum number of warm pods
	MaxPods int `json:"maxPods"`
	
	// Security level
	SecurityLevel string `json:"securityLevel,omitempty"`
	
	// Resource requirements
	Resources *ResourceRequirements `json:"resources,omitempty"`
}

// WarmPoolStatus defines the observed state of a WarmPool
type WarmPoolStatus struct {
	// Current number of warm pods
	CurrentPods int `json:"currentPods"`
	
	// Available warm pods
	AvailablePods int `json:"availablePods"`
	
	// Warm pod names
	WarmPods []string `json:"warmPods,omitempty"`
}

// RuntimeEnvironment represents a runtime environment
type RuntimeEnvironment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RuntimeEnvironmentSpec   `json:"spec,omitempty"`
	Status RuntimeEnvironmentStatus `json:"status,omitempty"`
}

// RuntimeEnvironmentSpec defines the desired state of a RuntimeEnvironment
type RuntimeEnvironmentSpec struct {
	// Base image
	BaseImage string `json:"baseImage"`
	
	// Language
	Language string `json:"language"`
	
	// Version
	Version string `json:"version"`
	
	// Pre-installed packages
	Packages []string `json:"packages,omitempty"`
}

// RuntimeEnvironmentStatus defines the observed state of a RuntimeEnvironment
type RuntimeEnvironmentStatus struct {
	// Whether the runtime environment is ready
	Ready bool `json:"ready"`
	
	// Last update time
	LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`
}

// SandboxProfile represents a sandbox profile
type SandboxProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SandboxProfileSpec `json:"spec,omitempty"`
}

// SandboxProfileSpec defines the desired state of a SandboxProfile
type SandboxProfileSpec struct {
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
}

// SandboxNotFoundError represents a sandbox not found error
type SandboxNotFoundError struct {
	ID string
}

// Error implements the error interface
func (e *SandboxNotFoundError) Error() string {
	return fmt.Sprintf("sandbox %s not found", e.ID)
}

// DeepCopy creates a deep copy of the Sandbox
func (s *Sandbox) DeepCopy() *Sandbox {
	if s == nil {
		return nil
	}
	
	out := new(Sandbox)
	s.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies the receiver into out
func (s *Sandbox) DeepCopyInto(out *Sandbox) {
	*out = *s
	out.TypeMeta = s.TypeMeta
	s.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	
	// Deep copy spec
	out.Spec = SandboxSpec{
		Runtime:       s.Spec.Runtime,
		SecurityLevel: s.Spec.SecurityLevel,
		Timeout:       s.Spec.Timeout,
	}
	
	if s.Spec.Resources != nil {
		out.Spec.Resources = &ResourceRequirements{
			CPU:             s.Spec.Resources.CPU,
			Memory:          s.Spec.Resources.Memory,
			EphemeralStorage: s.Spec.Resources.EphemeralStorage,
			GPU:             s.Spec.Resources.GPU,
		}
	}
	
	if s.Spec.NetworkAccess != nil {
		out.Spec.NetworkAccess = &NetworkAccess{
			Ingress: s.Spec.NetworkAccess.Ingress,
		}
		
		if len(s.Spec.NetworkAccess.Egress) > 0 {
			out.Spec.NetworkAccess.Egress = make([]EgressRule, len(s.Spec.NetworkAccess.Egress))
			for i, rule := range s.Spec.NetworkAccess.Egress {
				out.Spec.NetworkAccess.Egress[i] = EgressRule{
					Domain: rule.Domain,
				}
				
				if len(rule.Ports) > 0 {
					out.Spec.NetworkAccess.Egress[i].Ports = make([]PortRule, len(rule.Ports))
					for j, port := range rule.Ports {
						out.Spec.NetworkAccess.Egress[i].Ports[j] = PortRule{
							Port:     port.Port,
							Protocol: port.Protocol,
						}
					}
				}
			}
		}
	}
	
	// Deep copy status
	out.Status = SandboxStatus{
		Phase:    s.Status.Phase,
		PodName:  s.Status.PodName,
		PodStatus: s.Status.PodStatus,
		PodIP:    s.Status.PodIP,
		NodeName: s.Status.NodeName,
	}
	
	if s.Status.StartTime != nil {
		out.Status.StartTime = s.Status.StartTime.DeepCopy()
	}
	
	if s.Status.PodStartTime != nil {
		out.Status.PodStartTime = s.Status.PodStartTime.DeepCopy()
	}
	
	if s.Status.Resources != nil {
		out.Status.Resources = &ResourceStatus{
			CPUUsage:            s.Status.Resources.CPUUsage,
			MemoryUsage:         s.Status.Resources.MemoryUsage,
			EphemeralStorageUsage: s.Status.Resources.EphemeralStorageUsage,
		}
	}
	
	if s.Status.WarmPodRef != nil {
		out.Status.WarmPodRef = &WarmPodReference{
			Name:      s.Status.WarmPodRef.Name,
			Namespace: s.Status.WarmPodRef.Namespace,
		}
	}
	
	if len(s.Status.Conditions) > 0 {
		out.Status.Conditions = make([]SandboxCondition, len(s.Status.Conditions))
		for i, condition := range s.Status.Conditions {
			out.Status.Conditions[i] = SandboxCondition{
				Type:    condition.Type,
				Status:  condition.Status,
				Reason:  condition.Reason,
				Message: condition.Message,
			}
			
			if condition.LastTransitionTime != nil {
				out.Status.Conditions[i].LastTransitionTime = condition.LastTransitionTime.DeepCopy()
			}
		}
	}
	
	if len(s.Status.ContainerStatuses) > 0 {
		out.Status.ContainerStatuses = make([]ContainerStatus, len(s.Status.ContainerStatuses))
		for i, status := range s.Status.ContainerStatuses {
			out.Status.ContainerStatuses[i] = ContainerStatus{
				Name:         status.Name,
				Ready:        status.Ready,
				RestartCount: status.RestartCount,
				State:        status.State,
				Reason:       status.Reason,
				Message:      status.Message,
				ExitCode:     status.ExitCode,
			}
			
			if status.StartedAt != nil {
				out.Status.ContainerStatuses[i].StartedAt = status.StartedAt.DeepCopy()
			}
			
			if status.FinishedAt != nil {
				out.Status.ContainerStatuses[i].FinishedAt = status.FinishedAt.DeepCopy()
			}
		}
	}
	
	if s.Status.NetworkInfo != nil {
		out.Status.NetworkInfo = &NetworkInfo{
			PodIP:    s.Status.NetworkInfo.PodIP,
			HostIP:   s.Status.NetworkInfo.HostIP,
			Ingress:  s.Status.NetworkInfo.Ingress,
		}
		
		if len(s.Status.NetworkInfo.EgressDomains) > 0 {
			out.Status.NetworkInfo.EgressDomains = make([]string, len(s.Status.NetworkInfo.EgressDomains))
			copy(out.Status.NetworkInfo.EgressDomains, s.Status.NetworkInfo.EgressDomains)
		}
	}
	
	if len(s.Status.Events) > 0 {
		out.Status.Events = make([]Event, len(s.Status.Events))
		for i, event := range s.Status.Events {
			out.Status.Events[i] = Event{
				Type:    event.Type,
				Reason:  event.Reason,
				Message: event.Message,
				Count:   event.Count,
				Source:  event.Source,
			}
			
			if event.Time != nil {
				out.Status.Events[i].Time = event.Time.DeepCopy()
			}
		}
	}
}
