package tests

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	"github.com/stretchr/testify/assert"
)

func TestCORSMiddleware_AllowedOrigin(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	
	config := middleware.CORSConfig{
		AllowedOrigins:   []string{"https://example.com"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE"},
		AllowedHeaders:   []string{"Origin", "Content-Type", "Authorization"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           86400,
	}
	
	router := gin.New()
	router.Use(middleware.CORSMiddleware(config))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})
	
	// Execute with allowed origin
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://example.com", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "X-Request-ID", w.Header().Get("Access-Control-Expose-Headers"))
}

func TestCORSMiddleware_DisallowedOrigin(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	
	config := middleware.CORSConfig{
		AllowedOrigins: []string{"https://example.com"},
	}
	
	router := gin.New()
	router.Use(middleware.CORSMiddleware(config))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})
	
	// Execute with disallowed origin
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://evil.com")
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSMiddleware_PreflightRequest(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	
	config := middleware.CORSConfig{
		AllowedOrigins:   []string{"https://example.com"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Origin", "Content-Type", "Authorization"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           86400,
	}
	
	router := gin.New()
	router.Use(middleware.CORSMiddleware(config))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})
	
	// Execute preflight request
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("OPTIONS", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type, Authorization")
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "https://example.com", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, w.Header().Get("Access-Control-Allow-Methods"), "POST")
	assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "Content-Type")
	assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "Authorization")
	assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "86400", w.Header().Get("Access-Control-Max-Age"))
}

func TestCORSMiddleware_WildcardOrigin(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	
	config := middleware.CORSConfig{
		AllowedOrigins: []string{"*"},
	}
	
	router := gin.New()
	router.Use(middleware.CORSMiddleware(config))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})
	
	// Execute with any origin
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://any-domain.com")
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://any-domain.com", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSMiddleware_OptionsPassthrough(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	
	config := middleware.CORSConfig{
		AllowedOrigins:    []string{"https://example.com"},
		OptionsPassthrough: true,
	}
	
	router := gin.New()
	router.Use(middleware.CORSMiddleware(config))
	router.OPTIONS("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "custom options handler")
	})
	
	// Execute OPTIONS request
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("OPTIONS", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	router.ServeHTTP(w, req)
	
	// Assert that our custom handler was called
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "custom options handler")
	assert.Equal(t, "https://example.com", w.Header().Get("Access-Control-Allow-Origin"))
}
