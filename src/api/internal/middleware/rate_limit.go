package middleware

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/pkg/utilities"
	"golang.org/x/time/rate"
)

// [Keep all existing code exactly the same until the maskString function...]

// Remove the maskString function from this file and update references to use utilities.MaskString

// Updated code where maskString was called:
// In applyTokenBucketRateLimit:
log.Warn("Rate limit exceeded",
	"api_key", utilities.MaskString(apiKey),
	// ... rest of the logging call remains the same
)

// In applyFixedWindowRateLimit: 
log.Warn("Rate limit exceeded",
	"api_key", utilities.MaskString(apiKey),
	// ... rest of the logging call remains the same
)

// In applySlidingWindowRateLimit:
log.Warn("Rate limit exceeded",
	"api_key", utilities.MaskString(apiKey),
	// ... rest of the logging call remains the same
)
