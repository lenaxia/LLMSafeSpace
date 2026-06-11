// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	llm "github.com/lenaxia/llmsafespace/sdk/go"
	canary "github.com/lenaxia/llmsafespace/sdks/canary/go"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	run := canary.NewRunner("session-title", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
	defer cancel()
	runSessionTitle(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("session-title", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	runSessionTitle(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runSessionTitle(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	if cfg.LLMAPIKey == "" {
		run.OK("skipped: no LLM key")
		return
	}

	c := cfg.Client()

	ws, err := c.Workspaces.Create(ctx, llm.CreateWorkspaceRequest{
		Name: "canary-session-title", Runtime: "base", StorageSize: "1Gi",
	})
	if !run.AssertNoError(err, "p1-create-ws") {
		return
	}
	wsID := ws.ID
	defer func() { _ = c.Workspaces.Delete(context.Background(), wsID) }()

	phase := canary.WaitActive(ctx, c, wsID)
	run.Assert(phase == "Active", "p1-ws-active", fmt.Sprintf("got %q", phase))
	if phase != "Active" {
		return
	}

	sess, err := canary.EnsureSessionWithRetry(ctx, c, wsID, 5)
	if !run.AssertNoError(err, "p1-ensure-session") {
		return
	}
	sessionID := sess.SessionID
	run.Assert(sessionID != "", "p1-session-id", "")

	_, err = c.Sessions.SendMessage(ctx, wsID, sessionID, "What are the first 5 prime numbers?")
	run.AssertNoError(err, "p2-send-message")

	titleFound := false
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		sessions, err := c.Sessions.List(ctx, wsID)
		if err == nil {
			for _, s := range sessions {
				if s.ID == sessionID && s.Title != "" {
					titleFound = true
					run.OK("p3-title-populated")
					break
				}
			}
		}
		if titleFound {
			break
		}
		select {
		case <-ctx.Done():
			break
		case <-time.After(2 * time.Second):
		}
	}

	run.Assert(titleFound, "n1-title-timeout", "title never appeared within 20s polling window")
}
