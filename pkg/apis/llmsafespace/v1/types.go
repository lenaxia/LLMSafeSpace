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
	Phase        string             `json:"phase,omitempty"`
	Conditions   []SandboxCondition `json:"conditions,omitempty"`
	PodName      string             `json:"podName,omitempty"`
	PodNamespace string             `json:"podNamespace,omitempty"`
	StartTime    *metav1.Time       `json:"startTime,omitempty"`
	Endpoint     string             `json:"endpoint,omitempty"`
	Resources    *ResourceStatus    `json:"resources,omitempty"`
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
