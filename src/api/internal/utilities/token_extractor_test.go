package utilities

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestExtractToken(t *testing.T) {
	// Test cases
	testCases := []struct {
		name     string
		setup    func(*gin.Context)
		config   TokenExtractorConfig
		expected string
	}{
		{
			"Bearer token in header",
			func(c *gin.Context) {
				c.Request.Header.Set("Authorization", "Bearer token123")
			},
			DefaultTokenExtractorConfig(),
			"token123",
		},
		{
			"Plain token in header",
			func(c *gin.Context) {
				c.Request.Header.Set("Authorization", "token123")
			},
			DefaultTokenExtractorConfig(),
			"",
		},
		{
			"Custom header",
			func(c *gin.Context) {
				c.Request.Header.Set("X-API-Key", "token123")
			},
			TokenExtractorConfig{HeaderName: "X-API-Key"},
			"token123",
		},
		{
			"Query parameter",
			func(c *gin.Context) {
				c.Request.URL.RawQuery = "token=token123"
			},
			DefaultTokenExtractorConfig(),
			"token123",
		},
		{
			"Custom query parameter",
			func(c *gin.Context) {
				c.Request.URL.RawQuery = "api_key=token123"
			},
			TokenExtractorConfig{QueryParamName: "api_key"},
			"token123",
		},
		{
			"Cookie",
			func(c *gin.Context) {
				c.Request.AddCookie(&http.Cookie{Name: "auth_token", Value: "token123"})
			},
			DefaultTokenExtractorConfig(),
			"token123",
		},
		{
			"Custom cookie",
			func(c *gin.Context) {
				c.Request.AddCookie(&http.Cookie{Name: "session", Value: "token123"})
			},
			TokenExtractorConfig{CookieName: "session"},
			"token123",
		},
		{
			"No token",
			func(c *gin.Context) {},
			DefaultTokenExtractorConfig(),
			"",
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request, _ = http.NewRequest("GET", "/", nil)
			tc.setup(c)
			
			result := ExtractToken(c, tc.config)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestIsAPIKey(t *testing.T) {
	// Test cases
	testCases := []struct {
		name     string
		token    string
		prefix   string
		expected bool
	}{
		{"Valid API key", "api_12345", "api_", true},
		{"Not an API key", "jwt_token", "api_", false},
		{"Empty token", "", "api_", false},
		{"Empty prefix", "api_12345", "", false},
		{"Prefix only", "api_", "api_", true},
		{"Case sensitive", "API_12345", "api_", false},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := IsAPIKey(tc.token, tc.prefix)
			assert.Equal(t, tc.expected, result)
		})
	}
}
