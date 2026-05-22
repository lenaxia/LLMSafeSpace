package mocks

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	lmocks "github.com/lenaxia/llmsafespace/mocks/logger"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// MockFactory creates test fixtures.
type MockFactory struct{}

func NewMockFactory() *MockFactory { return &MockFactory{} }

func (f *MockFactory) NewLogger() *lmocks.MockLogger {
	return lmocks.NewMockLogger()
}

func (f *MockFactory) NewSandbox(name, namespace, runtime string) *v1.Sandbox {
	return &v1.Sandbox{
		TypeMeta:   metav1.TypeMeta{Kind: "Sandbox", APIVersion: "llmsafespace.dev/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, UID: "test-uid"},
		Spec: v1.SandboxSpec{
			Runtime:       runtime,
			SecurityLevel: "standard",
			Timeout:       300,
			Resources:     &v1.ResourceRequirements{CPU: "500m", Memory: "512Mi"},
		},
		Status: v1.SandboxStatus{Phase: "Running"},
	}
}

func (f *MockFactory) NewRuntimeEnvironment(name, language, version string) *v1.RuntimeEnvironment {
	return &v1.RuntimeEnvironment{
		TypeMeta:   metav1.TypeMeta{Kind: "RuntimeEnvironment", APIVersion: "llmsafespace.dev/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: "test-uid"},
		Spec: v1.RuntimeEnvironmentSpec{
			BaseImage: "llmsafespace/" + language + ":" + version,
			Language:  language,
			Version:   version,
		},
		Status: v1.RuntimeEnvironmentStatus{Ready: true},
	}
}

func (f *MockFactory) NewSandboxProfile(name string) *v1.SandboxProfile {
	return &v1.SandboxProfile{
		TypeMeta:   metav1.TypeMeta{Kind: "SandboxProfile", APIVersion: "llmsafespace.dev/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: "test-uid"},
		Spec: v1.SandboxProfileSpec{
			Resources: &v1.ResourceRequirements{CPU: "500m", Memory: "512Mi"},
		},
	}
}
