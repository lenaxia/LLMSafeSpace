package tests

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMain(m *testing.M) {
	// Set Gin to test mode
	gin.SetMode(gin.TestMode)
	
	// Run tests
	exitCode := m.Run()
	
	// Exit with the same code
	os.Exit(exitCode)
}
