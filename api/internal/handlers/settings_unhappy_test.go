package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- Admin settings unhappy paths ---

func TestAdminSettings_PUT_NullValue(t *testing.T) {
	r, _ := setupSettingsRouter("admin")

	body := `{"value": null}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/admin/settings/auth.registrationEnabled", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	// null is not a valid bool
	if w.Code != 400 {
		t.Errorf("expected 400 for null value on bool, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminSettings_PUT_WrongTypeForBool(t *testing.T) {
	r, _ := setupSettingsRouter("admin")

	body := `{"value": "yes"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/admin/settings/auth.registrationEnabled", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for string on bool setting, got %d", w.Code)
	}
}

func TestAdminSettings_PUT_WrongTypeForInt(t *testing.T) {
	r, _ := setupSettingsRouter("admin")

	body := `{"value": "ten"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/admin/settings/auth.lockoutAttempts", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for string on int setting, got %d", w.Code)
	}
}

func TestAdminSettings_PUT_FloatForInt(t *testing.T) {
	r, _ := setupSettingsRouter("admin")

	body := `{"value": 5.5}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/admin/settings/auth.lockoutAttempts", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for non-integer float, got %d", w.Code)
	}
}

func TestAdminSettings_PUT_EmptyBody(t *testing.T) {
	r, _ := setupSettingsRouter("admin")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/admin/settings/auth.lockoutAttempts", bytes.NewBufferString(""))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for empty body, got %d", w.Code)
	}
}

func TestAdminSettings_PUT_ArrayForBool(t *testing.T) {
	r, _ := setupSettingsRouter("admin")

	body := `{"value": [1,2,3]}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/admin/settings/auth.registrationEnabled", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for array on bool setting, got %d", w.Code)
	}
}

func TestAdminSettings_PUT_StringPatternReject(t *testing.T) {
	r, _ := setupSettingsRouter("admin")

	body := `{"value": "not-a-valid-size"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/admin/settings/workspace.defaultStorageSize", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for pattern mismatch, got %d", w.Code)
	}
}

func TestAdminSettings_PUT_ValidStoragePattern(t *testing.T) {
	r, _ := setupSettingsRouter("admin")

	body := `{"value": "512Mi"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/admin/settings/workspace.defaultStorageSize", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200 for valid storage pattern, got %d: %s", w.Code, w.Body.String())
	}
}

// --- User settings unhappy paths ---

func TestUserSettings_PUT_NullValue(t *testing.T) {
	r, _ := setupSettingsRouter("user")

	body := `{"value": null}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/users/me/settings/compactMode", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for null on bool, got %d", w.Code)
	}
}

func TestUserSettings_PUT_IntOutOfRange_Low(t *testing.T) {
	r, _ := setupSettingsRouter("user")

	body := `{"value": 5}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/users/me/settings/fontSize", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for fontSize=5 (min 10), got %d", w.Code)
	}
}

func TestUserSettings_PUT_IntOutOfRange_High(t *testing.T) {
	r, _ := setupSettingsRouter("user")

	body := `{"value": 30}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/users/me/settings/fontSize", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for fontSize=30 (max 24), got %d", w.Code)
	}
}

func TestUserSettings_PUT_ValidInt_Boundary(t *testing.T) {
	r, _ := setupSettingsRouter("user")

	for _, val := range []int{10, 24} {
		body, _ := json.Marshal(map[string]any{"value": val})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("PUT", "/api/v1/users/me/settings/fontSize", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Errorf("expected 200 for fontSize=%d (boundary), got %d", val, w.Code)
		}
	}
}

func TestUserSettings_PUT_BoolAsInt(t *testing.T) {
	r, _ := setupSettingsRouter("user")

	body := `{"value": 1}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/users/me/settings/compactMode", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for int on bool setting, got %d", w.Code)
	}
}

// --- Multiple sequential writes ---

func TestAdminSettings_SequentialWrites_LastWins(t *testing.T) {
	r, _ := setupSettingsRouter("admin")

	values := []int{10, 20, 30, 40, 50}
	for _, v := range values {
		body, _ := json.Marshal(map[string]any{"value": v})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("PUT", "/api/v1/admin/settings/auth.lockoutAttempts", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("PUT %d failed: %d", v, w.Code)
		}
	}

	// Read back — should be last value
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/admin/settings", nil)
	r.ServeHTTP(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	settings := resp["settings"].(map[string]any)
	if settings["auth.lockoutAttempts"] != float64(50) {
		t.Errorf("expected 50 (last write), got %v", settings["auth.lockoutAttempts"])
	}
}

// --- Schema response structure validation ---

func TestUserSettings_Schema_AllFieldsPresent(t *testing.T) {
	r, _ := setupSettingsRouter("user")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/users/me/settings/schema", nil)
	r.ServeHTTP(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	defs := resp["settings"].([]any)

	for _, d := range defs {
		def := d.(map[string]any)
		for _, field := range []string{"key", "tier", "type", "default", "category", "label", "description"} {
			if _, ok := def[field]; !ok {
				t.Errorf("user schema entry missing field %q: %v", field, def["key"])
			}
		}
		// Tier should be 3 for all user settings
		if def["tier"] != float64(3) {
			t.Errorf("user setting %v has tier %v, expected 3", def["key"], def["tier"])
		}
	}
}
