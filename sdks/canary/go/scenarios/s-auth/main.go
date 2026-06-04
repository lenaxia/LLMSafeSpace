// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Canary scenario: S-AUTH
// Tests valid API key, JWT login, and various invalid auth patterns.
package main

import (
	"context"
	"net/http"
	"os"
	"time"

	llm "github.com/lenaxia/llmsafespace/sdk/go"
	canary "github.com/lenaxia/llmsafespace/sdks/canary/go"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	run := canary.NewRunner("auth", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	runAuth(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("auth", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runAuth(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runAuth(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	c := cfg.Client()

	// P1: valid API key → Auth.Me fields
	me, err := c.Auth.Me(ctx)
	if run.AssertNoError(err, "valid-key: Auth.Me no error") {
		run.Assert(me["id"] != nil, "valid-key: user.id present", "")
		run.Assert(me["email"] != nil, "valid-key: user.email present", "")
		run.Assert(me["role"] != nil, "valid-key: user.role present", "")
		run.Assert(me["username"] != nil, "valid-key: user.username present", "")
		active, _ := me["active"].(bool)
		run.Assert(active, "valid-key: user.active is true", "")
	}

	// P2+P3: JWT login (if credentials configured)
	if cfg.Email != "" && cfg.Password != "" {
		jwtClient := llm.New(cfg.APIURL, llm.WithCredentials(cfg.Email, cfg.Password), llm.WithTimeout(20*time.Second))
		meJWT, err := jwtClient.Auth.Me(ctx)
		if run.AssertNoError(err, "jwt-login: Auth.Me no error") {
			run.Assert(meJWT["id"] != nil, "jwt-login: user.id present", "")
			active, _ := meJWT["active"].(bool)
			run.Assert(active, "jwt-login: user.active is true", "")
		}
	}

	// N1: invalid API key
	bad := cfg.BadClient()
	_, err = bad.Auth.Me(ctx)
	run.Assert(err != nil && llm.IsAuth(err),
		"invalid-key: returns AuthError", canary.ErrDetail(err, "expected IsAuth=true"))

	// N2: empty key
	noauth := llm.New(cfg.APIURL, llm.WithAPIKey(""), llm.WithTimeout(10*time.Second))
	_, err = noauth.Auth.Me(ctx)
	run.Assert(err != nil && llm.IsAuth(err),
		"empty-key: returns AuthError", canary.ErrDetail(err, "expected IsAuth=true"))

	// N3: malformed bearer
	garbage := llm.New(cfg.APIURL, llm.WithAPIKey("not-an-lsp-key"), llm.WithTimeout(10*time.Second))
	_, err = garbage.Auth.Me(ctx)
	run.Assert(err != nil && llm.IsAuth(err),
		"malformed-key: returns AuthError", canary.ErrDetail(err, "expected IsAuth=true"))

	// N4+N5: wrong/nonexistent email login — both must return same 401 shape (no enumeration)
	for _, test := range []struct {
		name, email, pw string
	}{
		{"wrong-password", cfg.Email, "definitely-wrong-password-xyz"},
		{"nonexistent-email", "canary-ghost-99@nonexistent.invalid", "wrongpassword123"},
	} {
		if test.email == "" {
			// Email not configured — record explicit skip so check count is consistent
			run.OK(test.name + ": skipped (LLMSAFESPACE_EMAIL not set)")
			continue
		}
		badLogin := llm.New(cfg.APIURL, llm.WithCredentials(test.email, test.pw), llm.WithTimeout(10*time.Second))
		_, err = badLogin.Auth.Me(ctx)
		run.Assert(err != nil && llm.IsAuth(err),
			test.name+": returns AuthError", canary.ErrDetail(err, "expected IsAuth=true"))
	}
}
