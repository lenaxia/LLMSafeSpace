package config

import "time"

// KubernetesConfig defines configuration for Kubernetes client
type KubernetesConfig struct {
	ConfigPath     string        `mapstructure:"configPath"`
	InCluster      bool          `mapstructure:"inCluster"`
	Namespace      string        `mapstructure:"namespace"`
	PodName        string        `mapstructure:"podName"`
	LeaderElection struct {
		Enabled       bool          `mapstructure:"enabled"`
		LeaseDuration time.Duration `mapstructure:"leaseDuration"`
		RenewDeadline time.Duration `mapstructure:"renewDeadline"`
		RetryPeriod   time.Duration `mapstructure:"retryPeriod"`
	} `mapstructure:"leaderElection"`
}
