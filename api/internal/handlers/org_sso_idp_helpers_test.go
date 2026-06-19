// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// The helpers below build a real RS256-signed OIDC ID token + JWKS document so
// the handler integration test exercises the genuine oidc.Verify path against a
// fake IdP. They mirror the fake IdP in the sso package test but live here
// because handler tests are in a different package.

func mustRSA(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}
	return k
}

func signRS256(t *testing.T, priv *rsa.PrivateKey, iss, aud, clientID string, extra map[string]any) (string, error) {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": iss, "sub": "handler-sub", "aud": aud,
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
	}
	for k, v := range extra {
		claims[k] = v
	}
	// Default email_verified=true (F8): a well-configured IdP verifies email
	// before asserting it. Production sso.Service REQUIRES the claim to be true
	// before binding an email to an account; tests that exercise the rejection
	// path pass email_verified=false explicitly in `extra`.
	if _, ok := claims["email_verified"]; !ok {
		if _, hasEmail := claims["email"]; hasEmail {
			claims["email_verified"] = true
		}
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "handler-kid-1"
	return tok.SignedString(priv)
}

func jwksJSON(t *testing.T, priv interface{}) string {
	t.Helper()
	k := priv.(*rsa.PrivateKey)
	n := base64.RawURLEncoding.EncodeToString(k.N.Bytes())
	eBytes := big.NewInt(int64(k.E)).Bytes()
	e := base64.RawURLEncoding.EncodeToString(eBytes)
	b, err := json.Marshal(map[string]any{
		"keys": []map[string]any{
			{"kty": "RSA", "kid": "handler-kid-1", "use": "sig", "alg": "RS256", "n": n, "e": e},
		},
	})
	if err != nil {
		t.Fatalf("marshal jwks: %v", err)
	}
	return string(b)
}

func base64urlDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// guard against fmt import being unused if the helpers evolve.
var _ = fmt.Sprintf
