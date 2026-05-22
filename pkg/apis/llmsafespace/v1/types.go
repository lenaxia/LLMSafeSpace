package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	GroupName    = "llmsafespace.dev"
	GroupVersion = "v1"
)

var SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: GroupVersion}

func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}

func AddToScheme(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&Sandbox{},
		&SandboxList{},
		&SandboxProfile{},
		&SandboxProfileList{},
		&RuntimeEnvironment{},
		&RuntimeEnvironmentList{},
		&Workspace{},
		&WorkspaceList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

type SandboxSpec struct {
	Runtime       string                `json:"runtime"`
	SecurityLevel string                `json:"securityLevel,omitempty"`
	Timeout       int                   `json:"timeout,omitempty"`
	Resources     *ResourceRequirements `json:"resources,omitempty"`
	NetworkAccess *NetworkAccess        `json:"networkAccess,omitempty"`
	Filesystem    *FilesystemConfig     `json:"filesystem,omitempty"`
	Storage       *StorageConfig        `json:"storage,omitempty"`
	SecurityCtx   *SecurityContext      `json:"securityContext,omitempty"`
	ProfileRef    *ProfileReference     `json:"profileRef,omitempty"`
	WorkspaceRef  string                `json:"workspaceRef,omitempty"`
}

type ResourceRequirements struct {
	CPU              string `json:"cpu,omitempty"`
	Memory           string `json:"memory,omitempty"`
	EphemeralStorage string `json:"ephemeralStorage,omitempty"`
}

type NetworkAccess struct {
	Egress  []EgressRule `json:"egress,omitempty"`
	Ingress bool         `json:"ingress,omitempty"`
}

type EgressRule struct {
	Domain string     `json:"domain"`
	Ports  []PortRule `json:"ports,omitempty"`
}

type PortRule struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol,omitempty"`
}

type FilesystemConfig struct {
	ReadOnlyRoot  bool     `json:"readOnlyRoot,omitempty"`
	WritablePaths []string `json:"writablePaths,omitempty"`
}

type StorageConfig struct {
	Persistent bool   `json:"persistent,omitempty"`
	VolumeSize string `json:"volumeSize,omitempty"`
}

type SecurityContext struct {
	RunAsUser      int64           `json:"runAsUser,omitempty"`
	RunAsGroup     int64           `json:"runAsGroup,omitempty"`
	SeccompProfile *SeccompProfile `json:"seccompProfile,omitempty"`
}

type SeccompProfile struct {
	Type             string `json:"type"`
	LocalhostProfile string `json:"localhostProfile,omitempty"`
}

type ProfileReference struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

type SandboxStatus struct {
	Phase          string             `json:"phase,omitempty"`
	Conditions     []SandboxCondition `json:"conditions,omitempty"`
	PodName        string             `json:"podName,omitempty"`
	PodNamespace   string             `json:"podNamespace,omitempty"`
	PodIP          string             `json:"podIP,omitempty"`
	StartTime      *metav1.Time       `json:"startTime,omitempty"`
	Endpoint       string             `json:"endpoint,omitempty"`
	Resources      *ResourceStatus    `json:"resources,omitempty"`
	LastActivityAt *metav1.Time       `json:"lastActivityAt,omitempty"`
}

type SandboxCondition struct {
	Type               string      `json:"type"`
	Status             string      `json:"status"`
	Reason             string      `json:"reason,omitempty"`
	Message            string      `json:"message,omitempty"`
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
}

type ResourceStatus struct {
	CPUUsage    string `json:"cpuUsage,omitempty"`
	MemoryUsage string `json:"memoryUsage,omitempty"`
}

type Sandbox struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SandboxSpec   `json:"spec,omitempty"`
	Status SandboxStatus `json:"status,omitempty"`
}

type SandboxList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Sandbox `json:"items"`
}

type SandboxProfileSpec struct {
	Resources     *ResourceRequirements `json:"resources,omitempty"`
	NetworkAccess *NetworkAccess        `json:"networkAccess,omitempty"`
	Filesystem    *FilesystemConfig     `json:"filesystem,omitempty"`
	Storage       *StorageConfig        `json:"storage,omitempty"`
	SecurityCtx   *SecurityContext      `json:"securityContext,omitempty"`
}

type SandboxProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SandboxProfileSpec `json:"spec,omitempty"`
}

type SandboxProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SandboxProfile `json:"items"`
}

type RuntimeEnvironmentSpec struct {
	BaseImage string   `json:"baseImage"`
	Language  string   `json:"language"`
	Version   string   `json:"version"`
	Packages  []string `json:"packages,omitempty"`
}

type RuntimeEnvironmentStatus struct {
	Ready          bool         `json:"ready"`
	LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`
}

type RuntimeEnvironment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RuntimeEnvironmentSpec   `json:"spec,omitempty"`
	Status RuntimeEnvironmentStatus `json:"status,omitempty"`
}

type RuntimeEnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RuntimeEnvironment `json:"items"`
}

type WorkspaceOwner struct {
	UserID string `json:"userID"`
}

type WorkspaceStorageConfig struct {
	Size             string `json:"size"`
	StorageClassName string `json:"storageClassName,omitempty"`
	AccessMode       string `json:"accessMode,omitempty"`
}

type WorkspaceAutoSuspend struct {
	Enabled            bool  `json:"enabled,omitempty"`
	IdleTimeoutSeconds int64 `json:"idleTimeoutSeconds,omitempty"`
}

type WorkspacePackageSet struct {
	Runtime      string   `json:"runtime"`
	Requirements []string `json:"requirements"`
}

type WorkspaceNetworkAccess struct {
	Egress  []WorkspaceEgressRule `json:"egress,omitempty"`
	Ingress bool                  `json:"ingress,omitempty"`
}

type WorkspaceEgressRule struct {
	Domain string `json:"domain"`
}

type WorkspaceCredentialRef struct {
	SecretName string `json:"secretName"`
}

type WorkspaceSpec struct {
	Owner                    WorkspaceOwner          `json:"owner"`
	DefaultRuntime           string                  `json:"defaultRuntime,omitempty"`
	SecurityLevel            string                  `json:"securityLevel,omitempty"`
	Storage                  WorkspaceStorageConfig  `json:"storage"`
	NetworkAccess            *WorkspaceNetworkAccess `json:"networkAccess,omitempty"`
	AutoSuspend              *WorkspaceAutoSuspend   `json:"autoSuspend,omitempty"`
	TTLSecondsAfterSuspended int64                   `json:"ttlSecondsAfterSuspended,omitempty"`
	Packages                 []WorkspacePackageSet   `json:"packages,omitempty"`
	InitScript               string                  `json:"initScript,omitempty"`
	MaxActiveSessions        int32                   `json:"maxActiveSessions,omitempty"`
	Credentials              *WorkspaceCredentialRef `json:"credentials,omitempty"`
}

type WorkspacePhase string

const (
	WorkspacePhasePending     WorkspacePhase = "Pending"
	WorkspacePhaseActive      WorkspacePhase = "Active"
	WorkspacePhaseSuspending  WorkspacePhase = "Suspending"
	WorkspacePhaseSuspended   WorkspacePhase = "Suspended"
	WorkspacePhaseResuming    WorkspacePhase = "Resuming"
	WorkspacePhaseTerminating WorkspacePhase = "Terminating"
	WorkspacePhaseTerminated  WorkspacePhase = "Terminated"
	WorkspacePhaseFailed      WorkspacePhase = "Failed"
)

type WorkspaceCondition struct {
	Type               string      `json:"type"`
	Status             string      `json:"status"`
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
	Reason             string      `json:"reason,omitempty"`
	Message            string      `json:"message,omitempty"`
}

type WorkspaceStatus struct {
	Phase              WorkspacePhase       `json:"phase,omitempty"`
	PVCName            string               `json:"pvcName,omitempty"`
	ActiveSessions     int32                `json:"activeSessions,omitempty"`
	LastActivityAt     *metav1.Time         `json:"lastActivityAt,omitempty"`
	SuspendedAt        *metav1.Time         `json:"suspendedAt,omitempty"`
	Conditions         []WorkspaceCondition `json:"conditions,omitempty"`
	Message            string               `json:"message,omitempty"`
	ObservedGeneration int64                `json:"observedGeneration,omitempty"`
}

type Workspace struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkspaceSpec   `json:"spec,omitempty"`
	Status WorkspaceStatus `json:"status,omitempty"`
}

type WorkspaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Workspace `json:"items"`
}
