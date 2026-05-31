// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAdminSettings_RoundTrip_WriteAndRead verifies that a PUT followed by GET
// returns the updated value (exercises the full service → store → cache path).
func TestAdminSettings_RoundTrip_WriteAndRead(t *testing.T) {
	r, _ := setupSettingsRouter("admin")

	// Write
	body := `{"value": false}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/admin/settings/auth.registrationEnabled", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("PUT failed: %d %s", w.Code, w.Body.String())
	}

	// Read
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/v1/admin/settings", nil)
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET failed: %d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	settings := resp["settings"].(map[string]any)
	if settings["auth.registrationEnabled"] != false {
		t.Errorf("expected false after write, got %v", settings["auth.registrationEnabled"])
	}
}

// TestAdminSettings_RoundTrip_IntValue tests int write/read round-trip.
func TestAdminSettings_RoundTrip_IntValue(t *testing.T) {
	r, _ := setupSettingsRouter("admin")

	body := `{"value": 42}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/admin/settings/auth.lockoutAttempts", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("PUT failed: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/v1/admin/settings", nil)
	r.ServeHTTP(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	settings := resp["settings"].(map[string]any)
	// JSON numbers come back as float64
	if settings["auth.lockoutAttempts"] != float64(42) {
		t.Errorf("expected 42 after write, got %v (%T)", settings["auth.lockoutAttempts"], settings["auth.lockoutAttempts"])
	}
}

// TestAdminSettings_RoundTrip_EnumValue tests enum write/read round-trip.
func TestAdminSettings_RoundTrip_EnumValue(t *testing.T) {
	r, _ := setupSettingsRouter("admin")

	body := `{"value": "sliding_window"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/admin/settings/rateLimiting.strategy", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("PUT failed: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/v1/admin/settings", nil)
	r.ServeHTTP(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	settings := resp["settings"].(map[string]any)
	if settings["rateLimiting.strategy"] != "sliding_window" {
		t.Errorf("expected sliding_window, got %v", settings["rateLimiting.strategy"])
	}
}

// TestUserSettings_RoundTrip_WriteAndRead verifies user settings persistence.
func TestUserSettings_RoundTrip_WriteAndRead(t *testing.T) {
	r, _ := setupSettingsRouter("user")

	body := `{"value": "dark"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/users/me/settings/theme", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("PUT failed: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/v1/users/me/settings", nil)
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET failed: %d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	settings := resp["settings"].(map[string]any)
	if settings["theme"] != "dark" {
		t.Errorf("expected dark after write, got %v", settings["theme"])
	}
}

// TestUserSettings_RoundTrip_IntValue tests int write/read for user settings.
func TestUserSettings_RoundTrip_IntValue(t *testing.T) {
	r, _ := setupSettingsRouter("user")

	body := `{"value": 20}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/users/me/settings/fontSize", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("PUT failed: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/v1/users/me/settings", nil)
	r.ServeHTTP(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	settings := resp["settings"].(map[string]any)
	if settings["fontSize"] != float64(20) {
		t.Errorf("expected 20, got %v", settings["fontSize"])
	}
}

// TestAdminSettings_PUT_MissingValueField tests that missing value field returns 400.
func TestAdminSettings_PUT_MissingValueField(t *testing.T) {
	r, _ := setupSettingsRouter("admin")

	body := `{}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/admin/settings/auth.lockoutAttempts", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for missing value, got %d", w.Code)
	}
}

// TestAdminSettings_PUT_InvalidJSON tests that malformed JSON returns 400.
func TestAdminSettings_PUT_InvalidJSON(t *testing.T) {
	r, _ := setupSettingsRouter("admin")

	body := `{not json`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/admin/settings/auth.lockoutAttempts", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for invalid JSON, got %d", w.Code)
	}
}

// TestUserSettings_PUT_MissingValueField tests that missing value field returns 400.
func TestUserSettings_PUT_MissingValueField(t *testing.T) {
	r, _ := setupSettingsRouter("user")

	body := `{}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/users/me/settings/theme", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for missing value, got %d", w.Code)
	}
}

// TestAdminSettings_Schema_ContainsExpectedFields verifies schema response structure.
func TestAdminSettings_Schema_ContainsExpectedFields(t *testing.T) {
	r, _ := setupSettingsRouter("admin")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/admin/settings/schema", nil)
	r.ServeHTTP(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	defs := resp["settings"].([]any)
	first := defs[0].(map[string]any)

	requiredFields := []string{"key", "tier", "type", "default", "category", "label", "description"}
	for _, field := range requiredFields {
		if _, exists := first[field]; !exists {
			t.Errorf("schema entry missing field %q", field)
		}
	}
}
