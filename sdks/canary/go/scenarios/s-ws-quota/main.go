// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Canary scenario: S-WS-QUOTA
// Tests workspace quota enforcement: creating workspaces up to the configured
// limit and verifying that the next creation returns 429.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	llm "github.com/lenaxia/llmsafespaces/sdk/go"
	canary "github.com/lenaxia/llmsafespaces/sdks/canary/go"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	run := canary.NewRunner("ws-quota", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	runWSQuota(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("ws-quota", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	runWSQuota(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runWSQuota(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	limitStr := os.Getenv("LLMSAFESPACES_MAX_WORKSPACES_PER_USER")
	limit := 10
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil {
			limit = v
		}
	}

	if limit == 0 {
		run.OK("quota disabled")
		return
	}

	c := cfg.Client()

	var created []string
	defer func() {
		bgCtx, bgCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer bgCancel()
		for _, id := range created {
			_ = c.Workspaces.Delete(bgCtx, id)
		}
	}()

	for i := 0; i < limit; i++ {
		ws, err := c.Workspaces.Create(ctx, llm.CreateWorkspaceRequest{
			Name:        fmt.Sprintf("canary-quota-%d", i),
			Runtime:     "base",
			StorageSize: "1Gi",
		})
		if !run.AssertNoError(err, fmt.Sprintf("P1: create workspace %d/%d", i+1, limit)) {
			return
		}
		created = append(created, ws.ID)
	}
	run.OK("P1: created up to configured limit")

	_, err := c.Workspaces.Create(ctx, llm.CreateWorkspaceRequest{
		Name:        "canary-quota-over",
		Runtime:     "base",
		StorageSize: "1Gi",
	})
	run.Assert(err != nil && llm.IsRateLimit(err),
		"N1: beyond limit returns 429",
		canary.ErrDetail(err, "expected IsRateLimit=true"))

	if err != nil {
		apiErr, ok := err.(*llm.APIError)
		if ok {
			var body map[string]any
			if jsonErr := json.Unmarshal([]byte(apiErr.Message), &body); jsonErr == nil {
				_, hasError := body["error"]
				_, hasLimit := body["limit"]
				run.Assert(hasError && hasLimit,
					"N2: 429 body has error and limit fields",
					fmt.Sprintf("error=%v limit=%v", hasError, hasLimit))
			} else {
				run.Fail("N2: 429 body has error and limit fields",
					fmt.Sprintf("message is not JSON: %s", apiErr.Message))
			}
		}
	}
}
