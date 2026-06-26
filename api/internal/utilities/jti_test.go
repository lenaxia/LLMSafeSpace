// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package utilities

import (
	"encoding/base64"
	"testing"
)

func TestExtractJTI_ValidToken(t *testing.T) {
	// Construct a minimal JWT with jti claim
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user-1","jti":"abc-123-def","exp":9999999999}`))
	sig := base64.RawURLEncoding.EncodeToString([]byte("fake-signature"))
	token := header + "." + payload + "." + sig

	jti := ExtractJTI(token)
	if jti != "abc-123-def" {
		t.Errorf("Expected 'abc-123-def', got '%s'", jti)
	}
}

func TestExtractJTI_NoJTI(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user-1"}`))
	sig := base64.RawURLEncoding.EncodeToString([]byte("sig"))
	token := header + "." + payload + "." + sig

	jti := ExtractJTI(token)
	if jti != "" {
		t.Errorf("Expected empty string for token without jti, got '%s'", jti)
	}
}

func TestExtractJTI_InvalidToken(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"no dots", "nodots"},
		{"one dot", "one.dot"},
		{"invalid base64", "x.!!!.y"},
		{"invalid json", "x." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".y"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jti := ExtractJTI(tt.token)
			if jti != "" {
				t.Errorf("Expected empty string for invalid token, got '%s'", jti)
			}
		})
	}
}

func TestExtractExp_ValidToken(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user-1","jti":"abc","exp":1234567890}`))
	sig := base64.RawURLEncoding.EncodeToString([]byte("sig"))
	token := header + "." + payload + "." + sig

	exp := ExtractExp(token)
	if exp != 1234567890 {
		t.Errorf("Expected exp=1234567890, got %d", exp)
	}
}

func TestExtractExp_NoExp(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"u-1"}`))
	sig := base64.RawURLEncoding.EncodeToString([]byte("sig"))
	token := header + "." + payload + "." + sig

	if got := ExtractExp(token); got != 0 {
		t.Errorf("Expected 0 for token without exp, got %d", got)
	}
}

func TestExtractExp_InvalidToken(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"no dots", "nope"},
		{"invalid base64 payload", "x.!!!.y"},
		{"invalid json payload", "x." + base64.RawURLEncoding.EncodeToString([]byte("garbage")) + ".y"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractExp(tt.token); got != 0 {
				t.Errorf("Expected 0 for invalid token, got %d", got)
			}
		})
	}
}
