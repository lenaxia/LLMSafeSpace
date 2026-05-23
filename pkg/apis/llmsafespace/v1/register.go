package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// GroupName is the group name for LLMSafeSpace CRDs.
	GroupName = "llmsafespace.dev"
	// GroupVersion is the version for LLMSafeSpace CRDs.
	GroupVersion = "v1"
)

// SchemeGroupVersion is the group version used to register these objects.
var SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: GroupVersion}

// Resource takes an unqualified resource and returns a Group qualified GroupResource.
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}

// AddToScheme adds all types of this clientset into the given scheme.
//
// metav1.AddToGroupVersion is REQUIRED for client-go's reflector to convert
// metav1.ListOptions and similar meta-types when listing/watching CRDs of
// this GroupVersion. Without it, watches fail with:
//
//	v1.ListOptions is not suitable for converting to "llmsafespace.dev/v1"
//
// — which silently kills the controller's reconcile loop with no resources.
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
