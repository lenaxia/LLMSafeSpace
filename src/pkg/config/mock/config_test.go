package mock

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewMockKubernetesConfig(t *testing.T) {
	// Create a mock Kubernetes config
	config := NewMockKubernetesConfig()
	
	// Verify the config values
	assert.Equal(t, "/mock/kubeconfig", config.ConfigPath)
	assert.False(t, config.InCluster)
	assert.Equal(t, "test-namespace", config.Namespace)
	assert.Equal(t, "test-pod", config.PodName)
	
	// Verify leader election config
	assert.False(t, config.LeaderElection.Enabled)
	assert.Equal(t, 15*time.Second, config.LeaderElection.LeaseDuration)
	assert.Equal(t, 10*time.Second, config.LeaderElection.RenewDeadline)
	assert.Equal(t, 2*time.Second, config.LeaderElection.RetryPeriod)
}

func TestNewMockConfig(t *testing.T) {
	// Create a mock config
	config := NewMockConfig()
	
	// Verify the config has a Kubernetes config
	assert.NotNil(t, config.Kubernetes)
	assert.Equal(t, "/mock/kubeconfig", config.Kubernetes.ConfigPath)
	assert.False(t, config.Kubernetes.InCluster)
	assert.Equal(t, "test-namespace", config.Kubernetes.Namespace)
}
