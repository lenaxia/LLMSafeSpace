// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Canary scenario: S-CRED-CRUD
// Tests LLM provider credential CRUD (stored as llm-provider secrets).
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	canary "github.com/lenaxia/llmsafespaces/sdks/canary/go"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	run := canary.NewRunner("cred-crud", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	runCredCRUD(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("cred-crud", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runCredCRUD(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runCredCRUD(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	c := cfg.JWTClient()

	// Build a valid llm-provider JSON value (placeholder key)
	credValue, _ := json.Marshal(map[string]string{
		"kind":    cfg.LLMProvider,
		"slug":    "canary-llm-cred",
		"apiKey":   "sk-canary-placeholder-key-00000000",
	})

	// P1: Create
	cred, err := c.Secrets.Create(ctx, "canary-llm-cred", "llm-provider", string(credValue))
	if !run.AssertNoError(err, "create-cred: no error") {
		return
	}
	run.Assert(cred.ID != "", "create-cred: id non-empty", "")
	run.Assert(cred.Type == "llm-provider", "create-cred: type=llm-provider", cred.Type)
	credID := cred.ID

	defer func() { _ = c.Secrets.Delete(context.Background(), credID) }()

	// P2: List — present
	list, err := c.Secrets.List(ctx)
	if run.AssertNoError(err, "list-creds: no error") {
		found := false
		for _, s := range list {
			if s.ID == credID {
				found = true
				break
			}
		}
		run.Assert(found, "list-creds: credential present", "")
	}

	// P3: Delete
	err = c.Secrets.Delete(ctx, credID)
	run.AssertNoError(err, "delete-cred: no error")

	listAfter, err := c.Secrets.List(ctx)
	if run.AssertNoError(err, "list-after-delete: no error") {
		gone := true
		for _, s := range listAfter {
			if s.ID == credID {
				gone = false
				break
			}
		}
		run.Assert(gone, "list-after-delete: absent", "")
	}

	// N1: Delete nonexistent
	err = c.Secrets.Delete(ctx, "00000000-0000-0000-0000-000000000097")
	run.Assert(err != nil, "delete-nonexistent-cred: error", canary.ErrDetail(err, "expected error"))

	// N2: Create with malformed JSON value — API should reject non-JSON value
	_, err = c.Secrets.Create(ctx, "canary-malformed-cred", "llm-provider", "not-valid-json")
	run.Assert(err != nil, "create-malformed-cred: error",
		canary.ErrDetail(err, "expected 400 for invalid JSON value"))

	// Fallback: if API accepts it (some deployments may not validate), just ensure it's a valid secret
	if err == nil {
		// Find and delete the accidentally-created secret
		list2, _ := c.Secrets.List(ctx)
		for _, s := range list2 {
			if s.Name == "canary-malformed-cred" {
				_ = c.Secrets.Delete(ctx, s.ID)
			}
		}
		run.Fail("create-malformed-cred: expected error but got none", "got nil (API accepted invalid JSON value)")
	}
}
