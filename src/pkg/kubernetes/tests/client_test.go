package tests

import (
	"testing"
	"time"

	"github.com/lenaxia/llmsafespace/pkg/config"
	"github.com/lenaxia/llmsafespace/pkg/kubernetes"
	"github.com/lenaxia/llmsafespace/pkg/logger"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/rest"
)

// TestNew tests the creation of a new Kubernetes client
func TestNew(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Test in-cluster configuration
	cfg := &config.KubernetesConfig{
		InCluster: true,
		Namespace: "test-namespace",
		PodName:   "test-pod",
		LeaderElection: struct {
			Enabled       bool          `mapstructure:"enabled"`
			LeaseDuration time.Duration `mapstructure:"leaseDuration"`
			RenewDeadline time.Duration `mapstructure:"renewDeadline"`
			RetryPeriod   time.Duration `mapstructure:"retryPeriod"`
		}{
			Enabled:       true,
			LeaseDuration: 15 * time.Second,
			RenewDeadline: 10 * time.Second,
			RetryPeriod:   2 * time.Second,
		},
	}

	// This test will fail in a non-Kubernetes environment, so we'll skip it
	// client, err := kubernetes.New(cfg, log)
	// if err != nil {
	//     t.Logf("Skipping in-cluster test: %v", err)
	// } else {
	//     assert.NotNil(t, client)
	// }

	// Test external configuration
	cfg.InCluster = false
	cfg.ConfigPath = "nonexistent"
	
	// This will fail because the kubeconfig doesn't exist
	client, err := kubernetes.New(cfg, log)
	assert.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "failed to build config from kubeconfig")
}

// TestClientLifecycle tests the client lifecycle methods
func TestClientLifecycle(t *testing.T) {
	// Create a mock client
	client := kubernetes.NewForTesting(nil, nil, &rest.Config{}, nil, nil)
	
	// Test Start and Stop methods
	err := client.Start()
	assert.NoError(t, err)
	
	client.Stop()
	// No assertions needed for Stop, just ensuring it doesn't panic
}

// TestClientAccessors tests the accessor methods of the client
func TestClientAccessors(t *testing.T) {
	// Create a client with mock components
	client := kubernetes.NewForTesting(nil, nil, &rest.Config{}, nil, nil)

	// Test accessors
	assert.Nil(t, client.Clientset())
	assert.Nil(t, client.DynamicClient())
	assert.NotNil(t, client.RESTConfig())
	assert.Nil(t, client.InformerFactory())
}
