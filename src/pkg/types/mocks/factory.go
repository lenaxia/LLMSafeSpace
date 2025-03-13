package mocks

import (
	"time"
	
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// Factory provides methods to create mock objects for testing
type Factory struct{}

// NewFactory creates a new mock factory
func NewFactory() *Factory {
	return &Factory{}
}

// CreateMockSandbox creates a mock Sandbox with the given name
func (f *Factory) CreateMockSandbox(name, namespace, runtime string) *types.Sandbox {
	return &types.Sandbox{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Sandbox",
			APIVersion: "llmsafespace.io/v1",
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

// CreateMockWarmPool creates a mock WarmPool with the given name
func (f *Factory) CreateMockWarmPool(name, namespace, runtime string) *types.WarmPool {
	return &types.WarmPool{
		TypeMeta: metav1.TypeMeta{
			Kind:       "WarmPool",
			APIVersion: "llmsafespace.io/v1",
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

// CreateMockWarmPod creates a mock WarmPod with the given name
func (f *Factory) CreateMockWarmPod(name, namespace, poolName string) *types.WarmPod {
	return &types.WarmPod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "WarmPod",
			APIVersion: "llmsafespace.io/v1",
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

// CreateMockRuntimeEnvironment creates a mock RuntimeEnvironment with the given name
func (f *Factory) CreateMockRuntimeEnvironment(name, language, version string) *types.RuntimeEnvironment {
	return &types.RuntimeEnvironment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RuntimeEnvironment",
			APIVersion: "llmsafespace.io/v1",
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

// CreateMockSandboxProfile creates a mock SandboxProfile with the given name
func (f *Factory) CreateMockSandboxProfile(name, language string) *types.SandboxProfile {
	return &types.SandboxProfile{
		TypeMeta: metav1.TypeMeta{
			Kind:       "SandboxProfile",
			APIVersion: "llmsafespace.io/v1",
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

// CreateMockExecutionResult creates a mock ExecutionResult
func (f *Factory) CreateMockExecutionResult(id string, exitCode int, stdout, stderr string) *types.ExecutionResult {
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

// CreateMockFileList creates a mock FileList
func (f *Factory) CreateMockFileList(path string, files []types.FileInfo) *types.FileList {
	return &types.FileList{
		Files: files,
		Path:  path,
		Total: len(files),
	}
}

// CreateMockFileInfo creates a mock FileInfo
func (f *Factory) CreateMockFileInfo(path string, isDir bool, size int64) types.FileInfo {
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

// CreateMockFileResult creates a mock FileResult
func (f *Factory) CreateMockFileResult(path string, size int64) *types.FileResult {
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
