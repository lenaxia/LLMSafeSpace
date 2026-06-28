// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package utilities

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// ExtractJTI extracts the jti (JWT ID) claim from a JWT token without
// full validation (validation is already done by ValidateToken).
// Returns empty string if extraction fails.
func ExtractJTI(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		JTI string `json:"jti"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.JTI
}

// ExtractExp returns the exp (expiration) claim of a JWT as a unix
// timestamp, without full signature validation (the caller has
// already validated the token via ValidateToken). Returns 0 if the
// token is malformed or the claim is missing — callers should treat
// 0 as "unknown" and fall back to a conservative default.
//
// Used by Epic 56 to size the soft-unlock durable row's TTL to the
// JWT's actual remaining lifetime, rather than a hardcoded ceiling.
func ExtractExp(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0
	}
	return claims.Exp
}
