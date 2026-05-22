package v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestSchemeRegistration(t *testing.T) {
	scheme := runtime.NewScheme()
	err := AddToScheme(scheme)
	require.NoError(t, err)

	gvk := schema.GroupVersionKind{Group: "llmsafespace.dev", Version: "v1"}

	tests := []struct {
		name    string
		obj     runtime.Object
		wantGVK string
	}{
		{"Sandbox", &Sandbox{}, "Sandbox"},
		{"SandboxList", &SandboxList{}, "SandboxList"},
		{"SandboxProfile", &SandboxProfile{}, "SandboxProfile"},
		{"SandboxProfileList", &SandboxProfileList{}, "SandboxProfileList"},
		{"RuntimeEnvironment", &RuntimeEnvironment{}, "RuntimeEnvironment"},
		{"RuntimeEnvironmentList", &RuntimeEnvironmentList{}, "RuntimeEnvironmentList"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gvks, _, _ := scheme.ObjectKinds(tt.obj)
			require.Len(t, gvks, 1, "expected exactly one GVK")
			assert.Equal(t, gvk.Group, gvks[0].Group)
			assert.Equal(t, gvk.Version, gvks[0].Version)
			assert.Equal(t, tt.wantGVK, gvks[0].Kind)
		})
	}
}

func TestSandboxDeepCopy(t *testing.T) {
	original := &Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sandbox",
			Namespace: "default",
		},
		Spec: SandboxSpec{
			Runtime:       "python:3.10",
			SecurityLevel: "standard",
			Timeout:       300,
			Resources: &ResourceRequirements{
				CPU:    "500m",
				Memory: "512Mi",
			},
			NetworkAccess: &NetworkAccess{
				Ingress: false,
				Egress: []EgressRule{
					{Domain: "pypi.org", Ports: []PortRule{{Port: 443, Protocol: "TCP"}}},
				},
			},
		},
		Status: SandboxStatus{
			Phase:   "Running",
			PodName: "test-sandbox-pod",
		},
	}

	copy := original.DeepCopy()

	assert.Equal(t, original.Spec.Runtime, copy.Spec.Runtime)
	assert.Equal(t, original.Spec.Resources.CPU, copy.Spec.Resources.CPU)
	assert.Equal(t, original.Status.Phase, copy.Status.Phase)

	copy.Spec.Runtime = "nodejs:18"
	copy.Spec.Resources.CPU = "1000m"
	copy.Status.Phase = "Failed"

	assert.Equal(t, "python:3.10", original.Spec.Runtime, "original should not be mutated")
	assert.Equal(t, "500m", original.Spec.Resources.CPU, "original should not be mutated")
	assert.Equal(t, "Running", original.Status.Phase, "original should not be mutated")
}

func TestSandboxDeepCopyObject(t *testing.T) {
	original := &Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec:       SandboxSpec{Runtime: "python:3.10"},
	}

	obj := original.DeepCopyObject()
	require.NotNil(t, obj)

	copy, ok := obj.(*Sandbox)
	require.True(t, ok, "DeepCopyObject should return *Sandbox")
	assert.Equal(t, original.Name, copy.Name)
	assert.Equal(t, original.Spec.Runtime, copy.Spec.Runtime)
}

func TestSandboxListDeepCopy(t *testing.T) {
	original := &SandboxList{
		Items: []Sandbox{
			{ObjectMeta: metav1.ObjectMeta{Name: "sandbox-1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "sandbox-2"}},
		},
	}

	copy := original.DeepCopy()
	assert.Len(t, copy.Items, 2)
	assert.Equal(t, "sandbox-1", copy.Items[0].Name)

	copy.Items[0].Name = "modified"
	assert.Equal(t, "sandbox-1", original.Items[0].Name, "original should not be mutated")
}

func TestSandboxNilSafeDeepCopy(t *testing.T) {
	var s *Sandbox
	assert.Nil(t, s.DeepCopy())

	var sl *SandboxList
	assert.Nil(t, sl.DeepCopy())
}

func TestSandboxProfileDeepCopy(t *testing.T) {
	original := &SandboxProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "default-profile"},
		Spec: SandboxProfileSpec{
			Resources: &ResourceRequirements{CPU: "1000m"},
		},
	}

	copy := original.DeepCopy()
	assert.Equal(t, "default-profile", copy.Name)
	assert.Equal(t, "1000m", copy.Spec.Resources.CPU)

	copy.Spec.Resources.CPU = "2000m"
	assert.Equal(t, "1000m", original.Spec.Resources.CPU)
}

func TestRuntimeEnvironmentDeepCopy(t *testing.T) {
	original := &RuntimeEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "python-310"},
		Spec: RuntimeEnvironmentSpec{
			BaseImage: "python:3.10-slim",
			Language:  "python",
			Version:   "3.10",
			Packages:  []string{"numpy", "pandas"},
		},
		Status: RuntimeEnvironmentStatus{
			Ready: true,
		},
	}

	copy := original.DeepCopy()
	assert.Equal(t, original.Spec.BaseImage, copy.Spec.BaseImage)
	assert.Equal(t, original.Spec.Packages, copy.Spec.Packages)

	copy.Spec.Packages[0] = "modified"
	assert.Equal(t, "numpy", original.Spec.Packages[0], "slice should be independently copied")
}

func TestGroupVersion(t *testing.T) {
	assert.Equal(t, "llmsafespace.dev", GroupName)
	assert.Equal(t, "v1", GroupVersion)
	assert.Equal(t, schema.GroupVersion{Group: "llmsafespace.dev", Version: "v1"}, SchemeGroupVersion)
}
