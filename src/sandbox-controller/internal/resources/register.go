package resources

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// GroupName is the group name for LLMSafeSpace CRDs
	GroupName = "llmsafespace.dev"
	// GroupVersion is the version for LLMSafeSpace CRDs
	GroupVersion = "v1"
)

var (
	// SchemeGroupVersion is the group version used to register these objects
	SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: GroupVersion}
)

// Resource takes an unqualified resource and returns a Group qualified GroupResource
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}

// AddToScheme adds all types of this clientset into the given scheme
func AddToScheme(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&Sandbox{},
		&SandboxList{},
		&SandboxProfile{},
		&SandboxProfileList{},
		&WarmPool{},
		&WarmPoolList{},
		&WarmPod{},
		&WarmPodList{},
		&RuntimeEnvironment{},
		&RuntimeEnvironmentList{},
	)
	return nil
}
