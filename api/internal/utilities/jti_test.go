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
