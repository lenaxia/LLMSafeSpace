// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Canary scenario: S-CRED-PROVIDER-PRECEDENCE
// Verifies the unified credential model (Epic 30) using the new
// /api/v1/provider-credentials and /api/v1/admin/provider-credentials endpoints.
package main

import (
	"context"
	"net/http"
	"os"
	"time"

	canary "github.com/lenaxia/llmsafespaces/sdks/canary/go"
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

	// P1: Admin list — verify free-tier credential was seeded at startup.
	adminCreds, err := c.AdminProviderCredentials.List(ctx)
	if !run.AssertNoError(err, "admin-list: no error") {
		return
	}
	found := false
	for _, cred := range adminCreds {
		if cred.Provider == "opencode" {
			found = true
			break
		}
	}
	run.Assert(found, "admin-list: free-tier opencode credential exists", "")

	// P2: User create — create a provider credential via the new endpoint.
	userCred, err := c.ProviderCredentials.Create(ctx, "canary-precedence", "canary-test", "canary-key-001", "")
	if !run.AssertNoError(err, "user-create: no error") {
		return
	}
	run.Assert(userCred.ID != "", "user-create: id non-empty", "")
	credID := userCred.ID

	defer func() { _ = c.ProviderCredentials.Delete(context.Background(), credID) }()

	// P3: User list — verify credential appears.
	list, err := c.ProviderCredentials.List(ctx)
	if run.AssertNoError(err, "user-list: no error") {
		listFound := false
		for _, cred := range list {
			if cred.ID == credID {
				listFound = true
				break
			}
		}
		run.Assert(listFound, "user-list: new credential present", "")
	}

	// P4: Delete credential.
	err = c.ProviderCredentials.Delete(ctx, credID)
	run.AssertNoError(err, "user-delete: no error")

	// N1: Get deleted credential — should fail.
	_, err = c.ProviderCredentials.Get(ctx, credID)
	run.Assert(err != nil, "get-deleted: returns error", canary.ErrDetail(err, "expected 404"))
}
