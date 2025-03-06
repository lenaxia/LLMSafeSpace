package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/lenaxia/llmsafespace/api/internal/app"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

func main() {
	// Load configuration
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config/config.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	log, err := logger.New(cfg.Logging.Development, cfg.Logging.Level, cfg.Logging.Encoding)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer log.Sync()

	// Create and initialize application
	application, err := app.New(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize application", err, nil)
	}

	// Setup signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start the application
	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Run()
	}()

	// Wait for signal or error
	select {
	case err := <-errCh:
		if err != nil {
			log.Fatal("Application error", err, nil)
		}
	case sig := <-sigCh:
		log.Info("Received signal, shutting down", "signal", sig.String())
		if err := application.Shutdown(); err != nil {
			log.Error("Error during shutdown", err, nil)
			os.Exit(1)
		}
	}
}
