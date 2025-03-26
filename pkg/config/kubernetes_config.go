package config

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	// Config is the main configuration structure
)

type Config struct {
	// Server configuration
	Server struct {
		Host            string          `mapstructure:"host"`
		Port            int             `mapstructure:"port"`
		ShutdownTimeout metav1.Duration `mapstructure:"shutdownTimeout"`
	} `mapstructure:"server"`

	// Kubernetes configuration
	Kubernetes KubernetesConfig `mapstructure:"kubernetes"`

	// Redis configuration
	Redis struct {
		Host     string `mapstructure:"host"`
		Port     int    `mapstructure:"port"`
		Password string `mapstructure:"password"`
		DB       int    `mapstructure:"db"`
		PoolSize int    `mapstructure:"poolSize"`
	} `mapstructure:"redis"`

	// Database configuration
	Database struct {
		Host     string `mapstructure:"host"`
		Port     int    `mapstructure:"port"`
		User     string `mapstructure:"user"`
		Password string `mapstructure:"password"`
		Database string `mapstructure:"database"`
		SSLMode  string `mapstructure:"sslMode"`
	} `mapstructure:"database"`

	// Auth configuration
	Auth struct {
		JWTSecret string `mapstructure:"jwtSecret"`
	} `mapstructure:"auth"`

	// Logging configuration
	Logging struct {
		Level       string `mapstructure:"level"`
		Format      string `mapstructure:"format"`
		Development bool   `mapstructure:"development"`
	} `mapstructure:"logging"`
}

// KubernetesConfig defines configuration for Kubernetes client
type KubernetesConfig struct {
	ConfigPath     string `mapstructure:"configPath"`
	InCluster      bool   `mapstructure:"inCluster"`
	Namespace      string `mapstructure:"namespace"`
	PodName        string `mapstructure:"podName"`
	LeaderElection struct {
		Enabled       bool            `mapstructure:"enabled"`
		LeaseDuration metav1.Duration `mapstructure:"leaseDuration"`
		RenewDeadline metav1.Duration `mapstructure:"renewDeadline"`
		RetryPeriod   metav1.Duration `mapstructure:"retryPeriod"`
	} `mapstructure:"leaderElection"`
}
