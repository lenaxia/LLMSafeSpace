// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Canary scenario: D-CRED-MODEL-FLOW
// The flagship end-to-end scenario:
// add credential → bind → set model → call agent (no reload) →
// call agent (new session / "reload") → cleanup.
// Requires LLMSAFESPACES_LLM_API_KEY and LLMSAFESPACES_LLM_MODEL.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	llm "github.com/lenaxia/llmsafespaces/sdk/go"
	canary "github.com/lenaxia/llmsafespaces/sdks/canary/go"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	run := canary.NewRunner("cred-model-flow", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 600*time.Second)
	defer cancel()
	runCredModelFlow(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("cred-model-flow", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	defer cancel()
	runCredModelFlow(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runCredModelFlow(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	if cfg.LLMAPIKey == "" || cfg.LLMModel == "" {
		run.OK("cred-model-flow: skipped (no LLM API key or model)")
		return
	}

	// IMPORTANT: secret injection requires a DEK (Data Encryption Key) that is
	// only unlocked when the request carries a JWT token. The DEK is keyed by
	// the JWT's jti claim. API key auth has no jti, so SetBindings →
	// pushSecretsToAgent → PrepareSecretsForInjection → GetDEK("") fails
	// silently, and the credential never reaches the running agent.
	//
	// Therefore this scenario MUST use JWT login (email+password) not an API key.
	// Without LLMSAFESPACES_EMAIL+PASSWORD, we skip the message tests and only
	// verify the API surface (bind, set model) works.
	jwtAvailable := cfg.Email != "" && cfg.Password != ""
	if !jwtAvailable {
		run.OK("cred-model-flow: JWT credentials not set — agent tests will be skipped (only API surface tested)")
	}

	// Use JWT client if available, API key client otherwise.
	var c *llm.Client
	if jwtAvailable {
		c = llm.New(cfg.APIURL, llm.WithCredentials(cfg.Email, cfg.Password), llm.WithTimeout(60*time.Second))
	} else {
		c = cfg.Client()
	}

	// Step 1: Create workspace, wait Active
	ws, err := c.Workspaces.Create(ctx, llm.CreateWorkspaceRequest{
		Name: "canary-cred-flow", Runtime: "base", StorageSize: "1Gi",
	})
	if !run.AssertNoError(err, "create-ws: no error") {
		return
	}
	wsID := ws.ID
	defer func() { _ = c.Workspaces.Delete(context.Background(), wsID) }()

	phase := canary.WaitActive(ctx, c, wsID)
	run.Assert(phase == "Active", "reach-active", fmt.Sprintf("got %q", phase))
	if phase != "Active" {
		return
	}

	// Step 2: Create LLM credential
	credValue, _ := json.Marshal(map[string]string{
		"provider": cfg.LLMProvider,
		"apiKey":   cfg.LLMAPIKey,
	})
	cred, err := c.Secrets.Create(ctx, "canary-flow-cred", "llm-provider", string(credValue))
	if !run.AssertNoError(err, "create-cred: no error") {
		return
	}
	run.Assert(cred.Type == "llm-provider", "create-cred: type=llm-provider", cred.Type)
	credID := cred.ID
	defer func() { _ = c.Secrets.Delete(context.Background(), credID) }()

	// Step 3: Bind
	err = c.Workspaces.SetBindings(ctx, wsID, []string{credID})
	run.AssertNoError(err, "bind-cred: no error")

	// Step 4: Set model
	err = c.Workspaces.SetModel(ctx, wsID, cfg.LLMModel)
	run.AssertNoError(err, "set-model: no error")

	// Steps 5–9 require JWT auth so the DEK is available for secret injection.
	// With API key only, SetBindings' internal pushSecretsToAgent silently fails
	// (no jti → no DEK → PrepareSecretsForInjection returns error), so the agent
	// never receives the credential and SendMessage will fail.
	if !jwtAvailable {
		run.OK("agent-tests: skipped (JWT required for DEK-based secret injection)")
		// Still delete the credential as cleanup
		err = c.Secrets.Delete(ctx, credID)
		run.AssertNoError(err, "delete-cred: no error")
		return
	}

	// Step 5: Ensure session
	sess, err := canary.EnsureSessionWithRetry(ctx, c, wsID, 5)
	if !run.AssertNoError(err, "ensure-session: no error") {
		return
	}
	sessionID := sess.SessionID

	// Step 6: Send message (first session — no explicit reload needed; SetBindings
	// already pushed the credential via pushSecretsToAgent since we have a JWT DEK)
	msg, err := c.Sessions.SendMessage(ctx, wsID, sessionID, "Reply with exactly: CRED-FLOW-OK")
	if run.AssertNoError(err, "send-message: no error") {
		run.Assert(len(msg.Content) > 0, "send-message: non-empty content", "")
		run.Assert(strings.Contains(strings.ToUpper(msg.Content), "CRED-FLOW-OK"),
			"send-message: contains expected text",
			fmt.Sprintf("content: %q", msg.Content[:min(len(msg.Content), 100)]))
	}

	// Step 7: History
	history, err := c.Sessions.GetHistory(ctx, wsID, sessionID)
	if run.AssertNoError(err, "history: no error") {
		run.Assert(len(history) >= 1, "history: ≥1 entry", fmt.Sprintf("got %d", len(history)))
	}

	// Step 8: Second session (simulates browser reload / new conversation)
	sess2, err := c.Sessions.Ensure(ctx, wsID)
	if !run.AssertNoError(err, "ensure-session-2: no error") {
		return
	}
	run.Assert(sess2.SessionID != "", "ensure-session-2: sessionId", "")

	// Step 9: Send to second session
	msg2, err := c.Sessions.SendMessage(ctx, wsID, sess2.SessionID, "Reply with exactly: AFTER-RELOAD")
	if run.AssertNoError(err, "send-message-2: no error") {
		run.Assert(len(msg2.Content) > 0, "send-message-2: non-empty content", "")
		run.Assert(strings.Contains(strings.ToUpper(msg2.Content), "AFTER-RELOAD"),
			"send-message-2: contains expected text",
			fmt.Sprintf("content: %q", msg2.Content[:min(len(msg2.Content), 100)]))
	}

	// Step 10: Delete credential
	err = c.Secrets.Delete(ctx, credID)
	run.AssertNoError(err, "delete-cred: no error")

	list, err := c.Secrets.List(ctx)
	if run.AssertNoError(err, "list-after-delete: no error") {
		gone := true
		for _, s := range list {
			if s.ID == credID {
				gone = false
				break
			}
		}
		run.Assert(gone, "delete-cred: absent from list", "")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
