package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"

	k8sconfig "github.com/lenaxia/llmsafespace/pkg/config"
)

// Config represents the application configuration
type Config struct {
	Server struct {
		Host            string        `mapstructure:"host"`
		Port            int           `mapstructure:"port"`
		ShutdownTimeout time.Duration `mapstructure:"shutdownTimeout"`
	} `mapstructure:"server"`

	// Use the shared Kubernetes config
	Kubernetes k8sconfig.KubernetesConfig `mapstructure:"kubernetes"`

	Database struct {
		Host            string        `mapstructure:"host"`
		Port            int           `mapstructure:"port"`
		User            string        `mapstructure:"user"`
		Password        string        `mapstructure:"password"`
		Database        string        `mapstructure:"database"`
		SSLMode         string        `mapstructure:"sslMode"`
		MaxOpenConns    int           `mapstructure:"maxOpenConns"`
		MaxIdleConns    int           `mapstructure:"maxIdleConns"`
		ConnMaxLifetime time.Duration `mapstructure:"connMaxLifetime"`
	} `mapstructure:"database"`

	Redis struct {
		Host     string `mapstructure:"host"`
		Port     int    `mapstructure:"port"`
		Password string `mapstructure:"password"`
		DB       int    `mapstructure:"db"`
		PoolSize int    `mapstructure:"poolSize"`
	} `mapstructure:"redis"`

	Auth struct {
		JWTSecret           string        `mapstructure:"jwtSecret"`
		TokenDuration       time.Duration `mapstructure:"tokenDuration"`
		APIKeyPrefix        string        `mapstructure:"apiKeyPrefix"`
		CookieName          string        `mapstructure:"cookieName"`
		RegistrationEnabled bool          `mapstructure:"registrationEnabled"`
		LockoutEnabled      bool          `mapstructure:"lockoutEnabled"`
		LockoutAttempts     int           `mapstructure:"lockoutAttempts"`
		LockoutDuration     time.Duration `mapstructure:"lockoutDuration"`
	} `mapstructure:"auth"`

	Security struct {
		AllowedOrigins   []string `mapstructure:"allowedOrigins"`
		AllowCredentials bool     `mapstructure:"allowCredentials"`
	} `mapstructure:"security"`

	Logging struct {
		Level       string `mapstructure:"level"`
		Development bool   `mapstructure:"development"`
		Encoding    string `mapstructure:"encoding"`
	} `mapstructure:"logging"`

	RateLimiting struct {
		Enabled       bool          `mapstructure:"enabled"`
		DefaultLimit  int           `mapstructure:"defaultLimit"`
		DefaultWindow time.Duration `mapstructure:"defaultWindow"`
		BurstSize     int           `mapstructure:"burstSize"`
		Strategy      string        `mapstructure:"strategy"`
	} `mapstructure:"rateLimiting"`
}

// Load loads configuration from file and environment variables
func Load(path string) (*Config, error) {
	var config Config

	// Set up viper
	v := viper.New()
	v.SetConfigType("yaml")

	// Read config file
	if path != "" {
		v.SetConfigFile(path)
	} else {
		// Look for config in default locations
		v.AddConfigPath("./config")
		v.AddConfigPath(".")
		v.SetConfigName("config")
	}

	// Read config file
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Set up environment variable overrides
	v.SetEnvPrefix("LLMSAFESPACE")
	v.AutomaticEnv()

	// Unmarshal config
	if err := v.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Override with environment variables for sensitive data
	if envDBPassword := os.Getenv("LLMSAFESPACE_DATABASE_PASSWORD"); envDBPassword != "" {
		config.Database.Password = envDBPassword
	}

	if envRedisPassword := os.Getenv("LLMSAFESPACE_REDIS_PASSWORD"); envRedisPassword != "" {
		config.Redis.Password = envRedisPassword
	}

	if envJWTSecret := os.Getenv("LLMSAFESPACE_AUTH_JWTSECRET"); envJWTSecret != "" {
		config.Auth.JWTSecret = envJWTSecret
	}

	if v := os.Getenv("LLMSAFESPACE_AUTH_LOCKOUTENABLED"); v == "true" {
		config.Auth.LockoutEnabled = true
	}
	if v := os.Getenv("LLMSAFESPACE_AUTH_LOCKOUTATTEMPTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			config.Auth.LockoutAttempts = n
		}
	}
	if v := os.Getenv("LLMSAFESPACE_AUTH_LOCKOUTDURATION"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			config.Auth.LockoutDuration = d
		}
	}

	if v := os.Getenv("LLMSAFESPACE_SECURITY_ALLOWEDORIGINS"); v != "" {
		config.Security.AllowedOrigins = strings.Split(v, ",")
	}
	if v := os.Getenv("LLMSAFESPACE_SECURITY_ALLOWCREDENTIALS"); v == "true" {
		config.Security.AllowCredentials = true
	}

	if v := os.Getenv("LLMSAFESPACE_RATELIMITING_ENABLED"); v == "true" {
		config.RateLimiting.Enabled = true
	}
	if v := os.Getenv("LLMSAFESPACE_RATELIMITING_DEFAULTLIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			config.RateLimiting.DefaultLimit = n
		}
	}
	if v := os.Getenv("LLMSAFESPACE_RATELIMITING_DEFAULTWINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			config.RateLimiting.DefaultWindow = d
		}
	}
	if v := os.Getenv("LLMSAFESPACE_RATELIMITING_BURSTSIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			config.RateLimiting.BurstSize = n
		}
	}

	// Pod identity for leader election. Set via the Downward API in the
	// chart (metadata.name → LLMSAFESPACE_KUBERNETES_PODNAME). Without
	// this, leader election panics with "Lock identity is empty".
	if envPodName := os.Getenv("LLMSAFESPACE_KUBERNETES_PODNAME"); envPodName != "" {
		config.Kubernetes.PodName = envPodName
	}

	// Defensive fallback: if PodName is still empty but leader election is
	// enabled, fall back to os.Hostname() (the pod's hostname matches its
	// name in Kubernetes by default). Better than panicking.
	if config.Kubernetes.LeaderElection.Enabled && config.Kubernetes.PodName == "" {
		if hn, err := os.Hostname(); err == nil && hn != "" {
			config.Kubernetes.PodName = hn
		}
	}

	return &config, nil
}
