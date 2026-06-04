// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Canary scenario: S-APIKEY
// Tests API key create, list, use, delete, and post-delete rejection.
package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	llm "github.com/lenaxia/llmsafespace/sdk/go"
	canary "github.com/lenaxia/llmsafespace/sdks/canary/go"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	run := canary.NewRunner("apikey", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	runAPIKey(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("apikey", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runAPIKey(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runAPIKey(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	c := cfg.Client()

	// P1: Create
	newKey, err := c.Auth.CreateAPIKey(ctx, "canary-test-key")
	if !run.AssertNoError(err, "create-key: no error") {
		return
	}
	run.Assert(newKey.Name == "canary-test-key", "create-key: name matches", newKey.Name)
	run.Assert(strings.HasPrefix(newKey.Key, "lsp_"), "create-key: starts with lsp_", newKey.Key)
	run.Assert(newKey.Active, "create-key: active=true", "")
	run.Assert(newKey.ID != "", "create-key: id non-empty", "")

	defer func() { _ = c.Auth.DeleteAPIKey(context.Background(), newKey.ID) }()

	// P2: key absent in list
	keys, err := c.Auth.ListAPIKeys(ctx)
	if run.AssertNoError(err, "list-keys: no error") {
		found := false
		for _, k := range keys {
			if k.ID == newKey.ID {
				found = true
				run.Assert(k.Key == "", "list-keys: key value absent in list", k.Key)
				break
			}
		}
		run.Assert(found, "list-keys: created key in list", "")
	}

	// P3: New key authenticates
	newKeyClient := llm.New(cfg.APIURL, llm.WithAPIKey(newKey.Key), llm.WithTimeout(10*time.Second))
	_, err = newKeyClient.Auth.Me(ctx)
	run.AssertNoError(err, "new-key: authenticates")

	// P4: Delete
	err = c.Auth.DeleteAPIKey(ctx, newKey.ID)
	run.AssertNoError(err, "delete-key: no error")

	// P5: Absent after delete
	keysAfter, err := c.Auth.ListAPIKeys(ctx)
	if run.AssertNoError(err, "list-after-delete: no error") {
		gone := true
		for _, k := range keysAfter {
			if k.ID == newKey.ID {
				gone = false
				break
			}
		}
		run.Assert(gone, "list-after-delete: key absent", "")
	}

	// P6: Deleted key rejected
	_, err = newKeyClient.Auth.Me(ctx)
	run.Assert(err != nil && llm.IsAuth(err),
		"deleted-key: AuthError", canary.ErrDetail(err, "expected 401"))

	// N1: Delete nonexistent
	err = c.Auth.DeleteAPIKey(ctx, "00000000-0000-0000-0000-000000000099")
	run.Assert(err != nil, "delete-nonexistent: error", canary.ErrDetail(err, "expected error"))
}
