package config

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/viper"
)

// Config represents the application configuration
type Config struct {
	Server struct {
		Host            string        `mapstructure:"host"`
		Port            int           `mapstructure:"port"`
		ShutdownTimeout time.Duration `mapstructure:"shutdownTimeout"`
	} `mapstructure:"server"`

	Kubernetes struct {
		ConfigPath string `mapstructure:"configPath"`
		InCluster  bool   `mapstructure:"inCluster"`
		Namespace  string `mapstructure:"namespace"`
	} `mapstructure:"kubernetes"`

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
		JWTSecret     string        `mapstructure:"jwtSecret"`
		TokenDuration time.Duration `mapstructure:"tokenDuration"`
		APIKeyPrefix  string        `mapstructure:"apiKeyPrefix"`
	} `mapstructure:"auth"`

	Logging struct {
		Level       string `mapstructure:"level"`
		Development bool   `mapstructure:"development"`
		Encoding    string `mapstructure:"encoding"`
	} `mapstructure:"logging"`

	RateLimiting struct {
		Enabled bool `mapstructure:"enabled"`
		Limits  map[string]struct {
			Requests int           `mapstructure:"requests"`
			Window   time.Duration `mapstructure:"window"`
		} `mapstructure:"limits"`
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

	return &config, nil
}
