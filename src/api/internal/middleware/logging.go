package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/pkg/utilities"
)

// [Keep all existing code exactly the same until the maskString function...]

// Remove the maskString function from this file and update references to use utilities.MaskString

// Updated code where maskString was called:
// In logRequest:
fields = append(fields, "api_key", utilities.MaskString(apiKey.(string)))

// In maskSensitiveFields:
data[k] = utilities.MaskString(fmt.Sprint(v))
