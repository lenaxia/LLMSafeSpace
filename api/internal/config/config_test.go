package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	// Create a temporary config file
	content := `
server:
  host: "127.0.0.1"
  port: 8080
  shutdownTimeout: 30s

kubernetes:
  configPath: "/path/to/kubeconfig"
  inCluster: false
  namespace: "test-namespace"
  podName: "test-pod"
  leaderElection:
    enabled: true
    leaseDuration: 15s
    renewDeadline: 10s
    retryPeriod: 2s

database:
  host: "localhost"
  port: 5432
  user: "testuser"
  password: "testpass"
  database: "testdb"
  sslMode: "disable"
  maxOpenConns: 10
  maxIdleConns: 5
  connMaxLifetime: 5m

redis:
  host: "localhost"
  port: 6379
  password: "testpass"
  db: 0
  poolSize: 10

auth:
  jwtSecret: "test-secret"
  tokenDuration: 24h
  apiKeyPrefix: "lsp_"

logging:
  level: "debug"
  development: true
  encoding: "console"

rateLimiting:
  enabled: true
  limits:
    default:
      requests: 100
      window: 1h
`
	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	// Test loading from file
	cfg, err := Load(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify loaded values
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("Expected Server.Host to be '127.0.0.1', got '%s'", cfg.Server.Host)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Expected Server.Port to be 8080, got %d", cfg.Server.Port)
	}
	if cfg.Server.ShutdownTimeout != 30*time.Second {
		t.Errorf("Expected Server.ShutdownTimeout to be 30s, got %v", cfg.Server.ShutdownTimeout)
	}

	if cfg.Kubernetes.Namespace != "test-namespace" {
		t.Errorf("Expected Kubernetes.Namespace to be 'test-namespace', got '%s'", cfg.Kubernetes.Namespace)
	}

	if cfg.Database.Host != "localhost" {
		t.Errorf("Expected Database.Host to be 'localhost', got '%s'", cfg.Database.Host)
	}
	if cfg.Database.Password != "testpass" {
		t.Errorf("Expected Database.Password to be 'testpass', got '%s'", cfg.Database.Password)
	}

	// Test environment variable override
	os.Setenv("LLMSAFESPACE_DATABASE_PASSWORD", "envpass")
	defer os.Unsetenv("LLMSAFESPACE_DATABASE_PASSWORD")

	cfg, err = Load(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to load config with env vars: %v", err)
	}

	if cfg.Database.Password != "envpass" {
		t.Errorf("Expected Database.Password to be overridden to 'envpass', got '%s'", cfg.Database.Password)
	}
}

func TestLoadConfigError(t *testing.T) {
	// Test with non-existent file
	_, err := Load("non-existent-file.yaml")
	if err == nil {
		t.Error("Expected error when loading non-existent file, got nil")
	}
}
