package mock

import (
	"time"

	"github.com/lenaxia/llmsafespace/pkg/config"
)

// NewMockKubernetesConfig creates a mock Kubernetes configuration for testing
func NewMockKubernetesConfig() config.KubernetesConfig {
	return config.KubernetesConfig{
		ConfigPath: "/mock/kubeconfig",
		InCluster:  false,
		Namespace:  "test-namespace",
		PodName:    "test-pod",
		LeaderElection: struct {
			Enabled       bool          `mapstructure:"enabled"`
			LeaseDuration time.Duration `mapstructure:"leaseDuration"`
			RenewDeadline time.Duration `mapstructure:"renewDeadline"`
			RetryPeriod   time.Duration `mapstructure:"retryPeriod"`
		}{
			Enabled:       false,
			LeaseDuration: 15 * time.Second,
			RenewDeadline: 10 * time.Second,
			RetryPeriod:   2 * time.Second,
		},
	}
}

// NewMockConfig creates a mock configuration for testing
func NewMockConfig() *config.Config {
	return &config.Config{
		Kubernetes: NewMockKubernetesConfig(),
	}
}
