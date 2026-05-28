package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lenaxia/llmsafespace/pkg/secrets"
)

// TestHandler_E2E_CreateListGetDeleteRoundTrip tests the full CRUD cycle via HTTP
func TestHandler_E2E_CreateListGetDeleteRoundTrip(t *testing.T) {
	router, _, _ := setupTestRouter(t)

	// Create
	createBody := `{"name":"e2e-secret","type":"llm-provider","value":"sk-e2e-test-key","metadata":{"provider":"openai","model":"gpt-4o"}}`
	cReq := httptest.NewRequest(http.MethodPost, "/api/v1/secrets", bytes.NewBufferString(createBody))
	cReq.Header.Set("Content-Type", "application/json")
	cw := httptest.NewRecorder()
	router.ServeHTTP(cw, cReq)

	if cw.Code != http.StatusCreated {
		t.Fatalf("Create: expected 201, got %d: %s", cw.Code, cw.Body.String())
	}

	var created secrets.SecretResponse
	json.Unmarshal(cw.Body.Bytes(), &created)

	// List — should contain the secret
	lReq := httptest.NewRequest(http.MethodGet, "/api/v1/secrets", nil)
	lw := httptest.NewRecorder()
	router.ServeHTTP(lw, lReq)

	var listResp struct {
		Secrets []secrets.SecretResponse `json:"secrets"`
	}
	json.Unmarshal(lw.Body.Bytes(), &listResp)
	found := false
	for _, s := range listResp.Secrets {
		if s.ID == created.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("Created secret not found in list")
	}

	// Get by ID
	gReq := httptest.NewRequest(http.MethodGet, "/api/v1/secrets/"+created.ID, nil)
	gw := httptest.NewRecorder()
	router.ServeHTTP(gw, gReq)

	if gw.Code != http.StatusOK {
		t.Fatalf("Get: expected 200, got %d", gw.Code)
	}
	var got secrets.SecretResponse
	json.Unmarshal(gw.Body.Bytes(), &got)
	if got.Name != "e2e-secret" {
		t.Errorf("Get name mismatch: %s", got.Name)
	}

	// Delete
	dReq := httptest.NewRequest(http.MethodDelete, "/api/v1/secrets/"+created.ID, nil)
	dw := httptest.NewRecorder()
	router.ServeHTTP(dw, dReq)

	if dw.Code != http.StatusNoContent {
		t.Fatalf("Delete: expected 204, got %d", dw.Code)
	}

	// Verify gone
	gReq2 := httptest.NewRequest(http.MethodGet, "/api/v1/secrets/"+created.ID, nil)
	gw2 := httptest.NewRecorder()
	router.ServeHTTP(gw2, gReq2)
	if gw2.Code != http.StatusNotFound {
		t.Errorf("After delete: expected 404, got %d", gw2.Code)
	}
}

// TestHandler_E2E_UpdateAndVerify tests update changes the stored value
func TestHandler_E2E_UpdateAndVerify(t *testing.T) {
	router, _, _ := setupTestRouter(t)

	// Create
	body := `{"name":"updatable-e2e","type":"env-secret","value":"old-value","metadata":{"var_name":"TEST_VAR"}}`
	cReq := httptest.NewRequest(http.MethodPost, "/api/v1/secrets", bytes.NewBufferString(body))
	cReq.Header.Set("Content-Type", "application/json")
	cw := httptest.NewRecorder()
	router.ServeHTTP(cw, cReq)

	var created secrets.SecretResponse
	json.Unmarshal(cw.Body.Bytes(), &created)

	// Update
	uBody := `{"value":"new-value"}`
	uReq := httptest.NewRequest(http.MethodPut, "/api/v1/secrets/"+created.ID, bytes.NewBufferString(uBody))
	uReq.Header.Set("Content-Type", "application/json")
	uw := httptest.NewRecorder()
	router.ServeHTTP(uw, uReq)

	if uw.Code != http.StatusNoContent {
		t.Fatalf("Update: expected 204, got %d: %s", uw.Code, uw.Body.String())
	}

	// Get — metadata should still be there
	gReq := httptest.NewRequest(http.MethodGet, "/api/v1/secrets/"+created.ID, nil)
	gw := httptest.NewRecorder()
	router.ServeHTTP(gw, gReq)

	var got secrets.SecretResponse
	json.Unmarshal(gw.Body.Bytes(), &got)
	var meta map[string]string
	json.Unmarshal(got.Metadata, &meta)
	if meta["var_name"] != "TEST_VAR" {
		t.Errorf("Metadata lost after update: %v", meta)
	}
}

// TestHandler_E2E_BindingFullCycle tests bind→get→rebind→unbind
func TestHandler_E2E_BindingFullCycle(t *testing.T) {
	router, _, _ := setupTestRouter(t)

	// Create 2 secrets
	ids := make([]string, 2)
	for i, name := range []string{"bind-a", "bind-b"} {
		body := `{"name":"` + name + `","type":"llm-provider","value":"v","metadata":{"provider":"x"}}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/secrets", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var resp secrets.SecretResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		ids[i] = resp.ID
	}

	// Bind both
	bindBody, _ := json.Marshal(map[string][]string{"secretIds": ids})
	bReq := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-bind-test/bindings", bytes.NewBuffer(bindBody))
	bReq.Header.Set("Content-Type", "application/json")
	bw := httptest.NewRecorder()
	router.ServeHTTP(bw, bReq)
	if bw.Code != http.StatusNoContent {
		t.Fatalf("Bind: expected 204, got %d: %s", bw.Code, bw.Body.String())
	}

	// Get bindings
	gReq := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-bind-test/bindings", nil)
	gw := httptest.NewRecorder()
	router.ServeHTTP(gw, gReq)
	var resp secrets.BindingsResponse
	json.Unmarshal(gw.Body.Bytes(), &resp)
	if len(resp.Bindings) != 2 {
		t.Fatalf("Expected 2 bindings, got %d", len(resp.Bindings))
	}

	// Rebind with only first
	rebindBody, _ := json.Marshal(map[string][]string{"secretIds": {ids[0]}})
	rReq := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-bind-test/bindings", bytes.NewBuffer(rebindBody))
	rReq.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, rReq)
	if rw.Code != http.StatusNoContent {
		t.Fatalf("Rebind: expected 204, got %d", rw.Code)
	}

	// Verify only 1 binding
	gReq2 := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-bind-test/bindings", nil)
	gw2 := httptest.NewRecorder()
	router.ServeHTTP(gw2, gReq2)
	json.Unmarshal(gw2.Body.Bytes(), &resp)
	if len(resp.Bindings) != 1 {
		t.Errorf("Expected 1 binding after rebind, got %d", len(resp.Bindings))
	}

	// Unbind all (empty array)
	emptyBind := `{"secretIds":[]}`
	eReq := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-bind-test/bindings", bytes.NewBufferString(emptyBind))
	eReq.Header.Set("Content-Type", "application/json")
	ew := httptest.NewRecorder()
	router.ServeHTTP(ew, eReq)
	if ew.Code != http.StatusNoContent {
		t.Fatalf("Unbind: expected 204, got %d", ew.Code)
	}

	// Verify empty
	gReq3 := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-bind-test/bindings", nil)
	gw3 := httptest.NewRecorder()
	router.ServeHTTP(gw3, gReq3)
	json.Unmarshal(gw3.Body.Bytes(), &resp)
	if len(resp.Bindings) != 0 {
		t.Errorf("Expected 0 bindings after unbind, got %d", len(resp.Bindings))
	}
}

// TestHandler_CreateSecret_AllTypesViaHTTP tests creating each secret type through HTTP
func TestHandler_CreateSecret_AllTypesViaHTTP(t *testing.T) {
	router, _, _ := setupTestRouter(t)

	tests := []struct {
		name     string
		body     string
		wantCode int
	}{
		{"llm-provider", `{"name":"llm","type":"llm-provider","value":"sk-123","metadata":{"provider":"anthropic"}}`, 201},
		{"ssh-key", `{"name":"ssh","type":"ssh-key","value":"key-data","metadata":{"key_type":"ed25519","host":"github.com"}}`, 201},
		{"git-credential", `{"name":"git","type":"git-credential","value":"ghp_xxx","metadata":{"host":"github.com"}}`, 201},
		{"secret-file", `{"name":"file","type":"secret-file","value":"cert","metadata":{"mount_path":"/app/cert.pem"}}`, 201},
		{"env-secret", `{"name":"env","type":"env-secret","value":"postgres://...","metadata":{"var_name":"DB_URL"}}`, 201},
		{"invalid-type", `{"name":"bad","type":"nope","value":"x"}`, 400},
		{"ssh-no-metadata", `{"name":"ssh2","type":"ssh-key","value":"x"}`, 400},
		{"file-no-path", `{"name":"file2","type":"secret-file","value":"x","metadata":{"other":"y"}}`, 400},
		{"env-no-var", `{"name":"env2","type":"env-secret","value":"x","metadata":{"other":"y"}}`, 400},
		{"empty-name", `{"name":"","type":"llm-provider","value":"x"}`, 400},
		{"missing-value", `{"name":"novalue","type":"llm-provider"}`, 400},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/secrets", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("Expected %d, got %d: %s", tt.wantCode, w.Code, w.Body.String())
			}
		})
	}
}

// TestHandler_ConcurrentRequests tests concurrent HTTP requests don't race
func TestHandler_ConcurrentRequests(t *testing.T) {
	router, _, _ := setupTestRouter(t)

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			name := string(rune('a'+idx)) + "-concurrent"
			body := `{"name":"` + name + `","type":"llm-provider","value":"v` + name + `","metadata":{"provider":"x"}}`
			req := httptest.NewRequest(http.MethodPost, "/api/v1/secrets", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusCreated {
				t.Errorf("Concurrent create %d: expected 201, got %d", idx, w.Code)
			}
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// List should have all 10
	req := httptest.NewRequest(http.MethodGet, "/api/v1/secrets", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var resp struct {
		Secrets []secrets.SecretResponse `json:"secrets"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Secrets) != 10 {
		t.Errorf("Expected 10 secrets after concurrent creates, got %d", len(resp.Secrets))
	}
}
