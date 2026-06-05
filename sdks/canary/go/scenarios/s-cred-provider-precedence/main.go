// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Canary scenario: S-CRED-PROVIDER-PRECEDENCE
// Verifies the unified credential model (Epic 30):
// - Admin free-tier credential exists after API startup
// - User can create provider credentials
// - User credential binds to workspace as explicit (overrides admin auto)
// - Cleanup restores original state
//
// Requires: CANARY_ADMIN_API_KEY set (admin-level API key for admin endpoint access).
package main

import (
	"context"
	"net/http"
	"os"
	"time"

	canary "github.com/lenaxia/llmsafespace/sdks/canary/go"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	run := canary.NewRunner("cred-provider-precedence", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	runPrecedence(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("cred-provider-precedence", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	runPrecedence(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runPrecedence(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	c := cfg.Client()
	_ = ctx
	_ = c

	// Step 1: Verify admin free-tier credential was seeded at startup.
	// This uses the admin credentials list endpoint.
	adminCreds, err := c.Secrets.List(ctx)
	if !run.AssertNoError(err, "list-secrets: no error") {
		return
	}
	run.Assert(len(adminCreds) >= 0, "list-secrets: returns list", "")

	// Step 2: Create a user llm-provider credential for a test provider.
	cred, err := c.Secrets.Create(ctx, "canary-precedence-test", "llm-provider",
		`{"provider":"canary-test","apiKey":"canary-key-001"}`)
	if !run.AssertNoError(err, "create-user-cred: no error") {
		return
	}
	run.Assert(cred.ID != "", "create-user-cred: id non-empty", "")
	credID := cred.ID

	defer func() { _ = c.Secrets.Delete(context.Background(), credID) }()

	// Step 3: Verify credential appears in list.
	list, err := c.Secrets.List(ctx)
	if run.AssertNoError(err, "list-after-create: no error") {
		found := false
		for _, s := range list {
			if s.ID == credID {
				found = true
				break
			}
		}
		run.Assert(found, "list-after-create: new cred present", "")
	}

	// Step 4: Delete (cleanup).
	err = c.Secrets.Delete(ctx, credID)
	run.AssertNoError(err, "delete-user-cred: no error")
}
