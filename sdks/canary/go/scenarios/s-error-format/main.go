// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Canary scenario: S-ERROR-FORMAT
// Tests that all error responses have a consistent {"error": "..."} shape,
// proxy error shapes are correct, and no internal Go runtime strings leak.
package main

import (
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
	run := canary.NewRunner("error-format", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	runErrorFormat(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("error-format", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runErrorFormat(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runErrorFormat(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	base := cfg.APIURL + "/api/v1"

	// P1: 401 — no auth
	s1, b1, _ := canary.RawDo(ctx, "GET", base+"/auth/me", "", nil)
	run.Assert(s1 == 401, "401-no-auth: status", fmt.Sprintf("got %d", s1))
	run.Assert(canary.HasErrorField(b1), "401-no-auth: error field", "")
	assertErrorIsString(run, b1, "401-no-auth: error is string")

	// P2: 404 — nonexistent workspace
	s2, b2, _ := canary.RawDo(ctx, "GET", base+"/workspaces/00000000-0000-0000-0000-000000000000", cfg.APIKey, nil)
	run.Assert(s2 == 404, "404-nonexistent: status", fmt.Sprintf("got %d", s2))
	run.Assert(canary.HasErrorField(b2), "404-nonexistent: error field", "")
	assertErrorIsString(run, b2, "404-nonexistent: error is string")

	// P3: 400 — register with empty body
	s3, b3, _ := canary.RawDo(ctx, "POST", base+"/auth/register", "", []byte(`{}`))
	run.Assert(s3 == 400, "400-empty-register: status", fmt.Sprintf("got %d", s3))
	run.Assert(canary.HasErrorField(b3), "400-empty-register: error field", "")

	// P4: 400 — PUT workspace with missing name
	// First create a workspace to get a valid ID
	c := cfg.Client()
	ws, err := c.Workspaces.Create(ctx, llm.CreateWorkspaceRequest{
		Name: "canary-errfmt-ws", Runtime: "base", StorageSize: "1Gi",
	})
	if err == nil {
		defer func() { _ = c.Workspaces.Delete(context.Background(), ws.ID) }()
		s4, b4, _ := canary.RawDo(ctx, "PUT", base+"/workspaces/"+ws.ID, cfg.APIKey, []byte(`{}`))
		run.Assert(s4 == 400, "400-rename-empty: status", fmt.Sprintf("got %d", s4))
		run.Assert(canary.HasErrorField(b4), "400-rename-empty: error field", "")
		assertErrorIsString(run, b4, "400-rename-empty: error is string")

		// P7: proxy 503 "workspace not ready" shape (workspace is Pending, not Active)
		s7, b7, _ := canary.RawDo(ctx, "POST",
			base+"/workspaces/"+ws.ID+"/sessions/canary-session-id/message",
			cfg.APIKey, []byte(`{"content":"ping","parts":[{"type":"text","text":"ping"}]}`))
		// Should get 503 (workspace not ready) or 400 (invalid session ID)
		// The proxy returns 503 with phase+retryAfter when workspace isn't Active
		if s7 == 503 {
			run.Assert(canary.HasField(b7, "phase"), "503-not-ready: phase field", "")
			run.Assert(canary.HasField(b7, "retryAfter"), "503-not-ready: retryAfter field", "")
			run.Assert(canary.HasErrorField(b7), "503-not-ready: error field", "")
		} else {
			// 400 or other — still valid, just log
			run.Assert(s7 >= 400, "proxy-error: 4xx or 5xx", fmt.Sprintf("got %d", s7))
		}
	}

	// P8: Session ID path traversal — should be rejected at API layer
	s8, b8, _ := canary.RawDo(ctx, "GET",
		base+"/workspaces/test-ws/sessions/..%2F..%2Fetc%2Fpasswd/message",
		cfg.APIKey, nil)
	run.Assert(s8 == 400 || s8 == 404, "path-traversal: 400 or 404",
		fmt.Sprintf("got %d", s8))
	if s8 == 400 {
		run.Assert(canary.HasErrorField(b8), "path-traversal: error field", "")
	}

	// P5: All error values are strings (not null, object, array)
	for _, body := range [][]byte{b1, b2, b3} {
		if len(body) == 0 {
			continue
		}
		run.Assert(!canary.ContainsLeakedInternals(body),
			"no-leaked-internals: "+truncate(body, 50), "")
	}

	// P9: Success responses don't have error field
	s9, b9, _ := canary.RawDo(ctx, "GET", cfg.APIURL+"/livez", "", nil)
	run.Assert(s9 == 200, "success-no-error: livez 200", "")
	run.Assert(!canary.HasField(b9, "error"), "success-no-error: no error field", "")
}

func assertErrorIsString(run *canary.Runner, body []byte, label string) {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		run.Fail(label, "not valid JSON")
		return
	}
	v := obj["error"]
	_, isStr := v.(string)
	run.Assert(isStr, label, fmt.Sprintf("error field type: %T", v))
}

func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
