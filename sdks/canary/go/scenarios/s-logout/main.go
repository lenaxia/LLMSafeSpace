// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Canary scenario: S-LOGOUT
// Tests POST /auth/logout invalidates the JWT in the revocation cache.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	llm "github.com/lenaxia/llmsafespaces/sdk/go"
	canary "github.com/lenaxia/llmsafespaces/sdks/canary/go"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	run := canary.NewRunner("logout", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	runLogout(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("logout", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runLogout(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runLogout(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	if cfg.Email == "" || cfg.Password == "" {
		run.OK("logout: skipped (no email/password configured)")
		return
	}

	// P1: Login and get a JWT token
	loginURL := cfg.APIURL + "/api/v1/auth/login"
	loginBody, _ := json.Marshal(map[string]string{"email": cfg.Email, "password": cfg.Password})
	status, body, err := canary.RawDo(ctx, "POST", loginURL, "", loginBody)
	if !run.AssertNoError(err, "login: no error") {
		return
	}
	run.Assert(status == 200, fmt.Sprintf("login: 200 (got %d)", status), string(body))

	var loginResp struct {
		Token string `json:"token"`
	}
	if !run.AssertNoError(json.Unmarshal(body, &loginResp), "login: parse response") {
		return
	}
	run.Assert(loginResp.Token != "", "login: token non-empty", "")
	jwtToken := loginResp.Token

	// P2: JWT works for Auth.Me before logout
	jwtClient := llm.New(cfg.APIURL, llm.WithAPIKey(jwtToken), llm.WithTimeout(10*time.Second))
	_, err = jwtClient.Auth.Me(ctx)
	run.AssertNoError(err, "pre-logout: Auth.Me succeeds with JWT")

	// P3: POST /auth/logout with the JWT
	logoutURL := cfg.APIURL + "/api/v1/auth/logout"
	req, _ := http.NewRequestWithContext(ctx, "POST", logoutURL, bytes.NewReader([]byte{}))
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	resp, err := http.DefaultClient.Do(req)
	if run.AssertNoError(err, "logout: request no error") {
		resp.Body.Close()
		run.Assert(resp.StatusCode == 204, fmt.Sprintf("logout: 204 (got %d)", resp.StatusCode), "")
	}

	// P4: Same JWT must now be rejected (revocation cache check)
	_, err = jwtClient.Auth.Me(ctx)
	run.Assert(err != nil && llm.IsAuth(err),
		"post-logout: JWT rejected (revocation works)",
		canary.ErrDetail(err, "expected 401"))

	// P5: Idempotent second logout
	req2, _ := http.NewRequestWithContext(ctx, "POST", logoutURL, bytes.NewReader([]byte{}))
	req2.Header.Set("Authorization", "Bearer "+jwtToken)
	resp2, err := http.DefaultClient.Do(req2)
	if run.AssertNoError(err, "logout-idempotent: no error") {
		resp2.Body.Close()
		run.Assert(resp2.StatusCode == 204, "logout-idempotent: 204", "")
	}

	// N1+N2: API key is NOT revoked by logout
	apiKeyClient := cfg.Client()
	_, err = apiKeyClient.Auth.Me(ctx)
	run.AssertNoError(err, "api-key: still valid after JWT logout")
}
