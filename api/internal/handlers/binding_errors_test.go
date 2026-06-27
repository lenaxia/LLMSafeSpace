// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// testBindModel is a fixture struct that exercises the slug, email, min,
// and required validators routed through Gin's binding engine.
type testBindModel struct {
	Name  string `json:"name"  binding:"required,min=2,max=100"`
	Slug  string `json:"slug"  binding:"required,min=2,max=50,slug"`
	Email string `json:"email" binding:"required,email"`
}

// TestBindingErrorResponse_FieldLevel verifies that a validator failure
// produces a details map keyed by JSON field name. The hyphen-in-slug case
// is the one that originally produced the opaque 400 the user reported.
func TestBindingErrorResponse_FieldLevel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/bind", func(c *gin.Context) {
		var req testBindModel
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, bindingErrorResponse(err, &req))
			return
		}
		c.Status(http.StatusNoContent)
	})

	cases := []struct {
		name       string
		body       string
		wantField  string
		wantSubstr string
	}{
		{
			name:       "underscore slug rejected",
			body:       `{"name":"Foo","slug":"my_org","email":"a@b.com"}`,
			wantField:  "slug",
			wantSubstr: "letters, digits, and single hyphens",
		},
		{
			name:       "leading hyphen slug rejected",
			body:       `{"name":"Foo","slug":"-foo","email":"a@b.com"}`,
			wantField:  "slug",
			wantSubstr: "letters, digits, and single hyphens",
		},
		{
			name:       "bad email rejected",
			body:       `{"name":"Foo","slug":"my-org","email":"not-an-email"}`,
			wantField:  "email",
			wantSubstr: "Invalid email address",
		},
		{
			name:       "missing required name",
			body:       `{"slug":"my-org","email":"a@b.com"}`,
			wantField:  "name",
			wantSubstr: "required",
		},
		{
			name:       "name too short",
			body:       `{"name":"X","slug":"my-org","email":"a@b.com"}`,
			wantField:  "name",
			wantSubstr: "at least 2",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/bind", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}

			var resp map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if resp["error"] != "validation failed" {
				t.Errorf("expected error 'validation failed', got %v", resp["error"])
			}
			details, ok := resp["details"].(map[string]any)
			if !ok {
				t.Fatalf("expected details map, got %s", w.Body.String())
			}
			msg, ok := details[tc.wantField].(string)
			if !ok {
				t.Fatalf("expected details[%q], got %s", tc.wantField, w.Body.String())
			}
			if !strings.Contains(msg, tc.wantSubstr) {
				t.Errorf("details[%q] = %q, want substring %q", tc.wantField, msg, tc.wantSubstr)
			}
		})
	}
}

// TestBindingErrorResponse_MalformedJSON verifies that a JSON syntax error
// (no struct binding possible) produces the generic body-level error rather
// than a misleading per-field map.
func TestBindingErrorResponse_MalformedJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/bind", func(c *gin.Context) {
		var req testBindModel
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, bindingErrorResponse(err, &req))
			return
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest("POST", "/bind", bytes.NewBufferString(`{not json`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["error"] != "invalid request body" {
		t.Errorf("expected 'invalid request body' for malformed JSON, got %v", resp["error"])
	}
	if _, hasDetails := resp["details"]; hasDetails {
		t.Errorf("malformed JSON must not return a details map (it has no fields to attribute), got %s", w.Body.String())
	}
}

// TestBindingErrorResponse_NilError documents the fallback when called with
// nil — defensive: never return nil so callers can pass straight to c.JSON.
func TestBindingErrorResponse_NilError(t *testing.T) {
	out := bindingErrorResponse(nil, &testBindModel{})
	if out == nil {
		t.Fatal("must not return nil")
	}
	if out["error"] != "invalid request body" {
		t.Errorf("nil error should fall back to generic body-level error, got %v", out)
	}
}

// TestBindingErrorResponse_UnknownErrorType verifies the fallback for errors
// that aren't validator.ValidationErrors or json syntax errors.
func TestBindingErrorResponse_UnknownErrorType(t *testing.T) {
	out := bindingErrorResponse(errors.New("something else"), &testBindModel{})
	if out["error"] != "invalid request body" {
		t.Errorf("unknown error type should fall back to generic body-level error, got %v", out)
	}
	if _, hasDetails := out["details"]; hasDetails {
		t.Errorf("unknown error type must not produce a details map, got %v", out)
	}
}
