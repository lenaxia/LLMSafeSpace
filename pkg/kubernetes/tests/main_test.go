package tests

import (
	"os"
	"testing"

	"github.com/lenaxia/llmsafespace/pkg/logger"
)

// TestMain is the entry point for all tests in this package
func TestMain(m *testing.M) {
	// Setup before running tests
	log, _ := logger.New(true, "debug", "console")
	log.Info("Starting Kubernetes package tests")
	
	// Run tests
	exitCode := m.Run()
	
	// Cleanup after tests
	log.Info("Finished Kubernetes package tests")
	
	// Exit with the same code as the tests
	os.Exit(exitCode)
}
