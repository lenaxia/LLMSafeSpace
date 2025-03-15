package tests

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestMain(m *testing.M) {
	// Set Gin to test mode
	gin.SetMode(gin.TestMode)
	
	// Run tests
	exitCode := m.Run()
	
	// Exit with the same code
	os.Exit(exitCode)
}
