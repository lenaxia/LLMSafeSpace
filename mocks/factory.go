package mocks

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	kmocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	lmocks "github.com/lenaxia/llmsafespace/mocks/logger"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// MockFactory provides methods to create mock objects for testing
type MockFactory struct{}

// NewMockFactory creates a new mock factory
func NewMockFactory() *MockFactory {
	return &MockFactory{}
}

// NewKubernetesClient creates a new mock Kubernetes client
func (f *MockFactory) NewKubernetesClient() *kmocks.MockKubernetesClient {
	client := kmocks.NewMockKubernetesClient()
	// Set up common defaults
	client.On("Clientset").Return(fake.NewSimpleClientset())
	client.On("RESTConfig").Return(&rest.Config{})
	client.On("Start").Return(nil)
	client.On("Stop").Return()
	
	return client
}

// NewLogger creates a new mock logger
func (f *MockFactory) NewLogger() *lmocks.MockLogger {
	return lmocks.NewMockLogger()
}

// NewSandbox creates a mock Sandbox with the given name
func (f *MockFactory) NewSandbox(name, namespace, runtime string) *types.Sandbox {
	return &types.Sandbox{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Sandbox",
			APIVersion: "llmsafespace.dev/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "test-uid",
		},
		Spec: types.SandboxSpec{
			Runtime:       runtime,
			SecurityLevel: "standard",
			Timeout:       300,
			Resources: &types.ResourceRequirements{
				CPU:    "500m",
				Memory: "512Mi",
			},
		},
		Status: types.SandboxStatus{
			Phase:    "Running",
			PodName:  name + "-pod",
			Endpoint: "http://localhost:8080",
			StartTime: &metav1.Time{
				Time: time.Now(),
			},
		},
	}
}

// NewWarmPool creates a mock WarmPool with the given name
func (f *MockFactory) NewWarmPool(name, namespace, runtime string) *types.WarmPool {
	return &types.WarmPool{
		TypeMeta: metav1.TypeMeta{
			Kind:       "WarmPool",
			APIVersion: "llmsafespace.dev/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "test-uid",
		},
		Spec: types.WarmPoolSpec{
			Runtime:       runtime,
			MinSize:       3,
			MaxSize:       10,
			SecurityLevel: "standard",
		},
		Status: types.WarmPoolStatus{
			AvailablePods: 3,
			AssignedPods:  0,
			PendingPods:   0,
		},
	}
}

// NewWarmPod creates a mock WarmPod with the given name
func (f *MockFactory) NewWarmPod(name, namespace, poolName string) *types.WarmPod {
	return &types.WarmPod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "WarmPod",
			APIVersion: "llmsafespace.dev/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "test-uid",
		},
		Spec: types.WarmPodSpec{
			PoolRef: types.PoolReference{
				Name:      poolName,
				Namespace: namespace,
			},
			CreationTimestamp: &metav1.Time{
				Time: time.Now(),
			},
		},
		Status: types.WarmPodStatus{
			Phase:        "Ready",
			PodName:      name + "-pod",
			PodNamespace: namespace,
		},
	}
}

// NewRuntimeEnvironment creates a mock RuntimeEnvironment with the given name
func (f *MockFactory) NewRuntimeEnvironment(name, language, version string) *types.RuntimeEnvironment {
	return &types.RuntimeEnvironment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RuntimeEnvironment",
			APIVersion: "llmsafespace.dev/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  "test-uid",
		},
		Spec: types.RuntimeEnvironmentSpec{
			Image:    "llmsafespace/" + language + ":" + version,
			Language: language,
			Version:  version,
		},
		Status: types.RuntimeEnvironmentStatus{
			Available: true,
			LastValidated: &metav1.Time{
				Time: time.Now(),
			},
		},
	}
}

// NewSandboxProfile creates a mock SandboxProfile with the given name
func (f *MockFactory) NewSandboxProfile(name, language string) *types.SandboxProfile {
	return &types.SandboxProfile{
		TypeMeta: metav1.TypeMeta{
			Kind:       "SandboxProfile",
			APIVersion: "llmsafespace.dev/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  "test-uid",
		},
		Spec: types.SandboxProfileSpec{
			Language:      language,
			SecurityLevel: "standard",
		},
	}
}

// NewExecutionResult creates a mock ExecutionResult
func (f *MockFactory) NewExecutionResult(id string, exitCode int, stdout, stderr string) *types.ExecutionResult {
	now := time.Now()
	return &types.ExecutionResult{
		ID:          id,
		Status:      "completed",
		StartedAt:   now.Add(-1 * time.Second),
		CompletedAt: now,
		ExitCode:    exitCode,
		Stdout:      stdout,
		Stderr:      stderr,
	}
}

// NewFileList creates a mock FileList
func (f *MockFactory) NewFileList(path string, files []types.FileInfo) *types.FileList {
	return &types.FileList{
		Files: files,
		Path:  path,
		Total: len(files),
	}
}

// MockFileInfo creates a mock FileInfo
func MockFileInfo(path string, size int64, isDir bool) types.FileInfo {
	now := time.Now()
	return types.FileInfo{
		Path:      path,
		Name:      path[len(path)-1:],
		Size:      size,
		IsDir:     isDir,
		CreatedAt: now.Add(-24 * time.Hour),
		UpdatedAt: now,
		Mode:      0644,
		Owner:     "user",
		Group:     "group",
	}
}

// NewFileResult creates a mock FileResult
func (f *MockFactory) NewFileResult(path string, size int64) *types.FileResult {
	now := time.Now()
	return &types.FileResult{
		Path:      path,
		Size:      size,
		IsDir:     false,
		CreatedAt: now.Add(-24 * time.Hour),
		UpdatedAt: now,
		Checksum:  "mock-checksum",
	}
}
