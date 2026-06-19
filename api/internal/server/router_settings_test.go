// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/lenaxia/llmsafespaces/api/internal/handlers"
	"github.com/lenaxia/llmsafespaces/api/internal/middleware"
	"github.com/lenaxia/llmsafespaces/pkg/settings"
)

// TestSettingsRoutes_AdminGuard_BlocksNonAdmin tests that admin settings
// routes return 404 for non-admin users (via AdminGuard).
func TestSettingsRoutes_AdminGuard_BlocksNonAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &memSettingsStore{
		instance: make(map[string]json.RawMessage),
		users:    make(map[string]map[string]json.RawMessage),
	}
	instanceSvc := settings.NewInstanceService(store, nil)
	userSvc := settings.NewUserService(store, nil)
	h := handlers.NewSettingsHandler(instanceSvc, userSvc)

	r := gin.New()
	// Simulate authenticated non-admin user
	r.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Set("userRole", "user")
		c.Next()
	})
	admin := r.Group("/api/v1/admin/settings")
	admin.Use(middleware.AdminGuard())
	admin.GET("", h.GetAdminSettings)
	admin.GET("/schema", h.GetAdminSettingsSchema)
	admin.PUT("/:key", h.SetAdminSetting)

	tests := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/admin/settings"},
		{"GET", "/api/v1/admin/settings/schema"},
		{"PUT", "/api/v1/admin/settings/auth.lockoutAttempts"},
	}
	for _, tt := range tests {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(tt.method, tt.path, nil)
		r.ServeHTTP(w, req)
		assert.Equal(t, 404, w.Code, "%s %s should return 404 for non-admin", tt.method, tt.path)
	}
}

// TestSettingsRoutes_AdminGuard_AllowsAdmin tests that admin settings
// routes work for admin users.
func TestSettingsRoutes_AdminGuard_AllowsAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &memSettingsStore{
		instance: make(map[string]json.RawMessage),
		users:    make(map[string]map[string]json.RawMessage),
	}
	instanceSvc := settings.NewInstanceService(store, nil)
	userSvc := settings.NewUserService(store, nil)
	h := handlers.NewSettingsHandler(instanceSvc, userSvc)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "admin-1")
		c.Set("userRole", "admin")
		c.Next()
	})
	admin := r.Group("/api/v1/admin/settings")
	admin.Use(middleware.AdminGuard())
	admin.GET("", h.GetAdminSettings)
	admin.GET("/schema", h.GetAdminSettingsSchema)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/admin/settings", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/v1/admin/settings/schema", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
}

// TestSettingsRoutes_UserEndpoints_Work tests that user settings endpoints
// function correctly for authenticated users.
func TestSettingsRoutes_UserEndpoints_Work(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &memSettingsStore{
		instance: make(map[string]json.RawMessage),
		users:    make(map[string]map[string]json.RawMessage),
	}
	instanceSvc := settings.NewInstanceService(store, nil)
	userSvc := settings.NewUserService(store, nil)
	h := handlers.NewSettingsHandler(instanceSvc, userSvc)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Set("userRole", "user")
		c.Next()
	})
	user := r.Group("/api/v1/users/me/settings")
	user.GET("", h.GetUserSettings)
	user.GET("/schema", h.GetUserSettingsSchema)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/users/me/settings", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	settingsMap := resp["settings"].(map[string]any)
	assert.Equal(t, len(settings.UserSettings()), len(settingsMap))
}

// memSettingsStore implements settings.InstanceStore and settings.UserStore.
type memSettingsStore struct {
	instance map[string]json.RawMessage
	users    map[string]map[string]json.RawMessage
}

func (s *memSettingsStore) GetAllInstanceSettings(_ context.Context) (map[string]json.RawMessage, error) {
	return s.instance, nil
}
func (s *memSettingsStore) SetInstanceSetting(_ context.Context, key string, value json.RawMessage) error {
	s.instance[key] = value
	return nil
}
func (s *memSettingsStore) GetAllUserSettings(_ context.Context, userID string) (map[string]json.RawMessage, error) {
	if s.users[userID] == nil {
		return map[string]json.RawMessage{}, nil
	}
	return s.users[userID], nil
}
func (s *memSettingsStore) SetUserSetting(_ context.Context, userID, key string, value json.RawMessage) error {
	if s.users[userID] == nil {
		s.users[userID] = make(map[string]json.RawMessage)
	}
	s.users[userID][key] = value
	return nil
}
