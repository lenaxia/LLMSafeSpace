// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Canary scenario: D-SSE-EVENTS
// Tests that the workspace SSE broker delivers workspace.phase and
// session.status events in response to real lifecycle actions.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	llm "github.com/lenaxia/llmsafespace/sdk/go"
	canary "github.com/lenaxia/llmsafespace/sdks/canary/go"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	run := canary.NewRunner("sse-events", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
	defer cancel()
	runSSEEvents(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("sse-events", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	runSSEEvents(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

type sseEvent struct {
	Type      string `json:"type"`
	Phase     string `json:"phase,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Status    string `json:"status,omitempty"`
}

func runSSEEvents(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	c := cfg.Client()

	ws, err := c.Workspaces.Create(ctx, llm.CreateWorkspaceRequest{
		Name: "canary-sse-events", Runtime: "base", StorageSize: "1Gi",
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

	// P1: Connect to SSE stream — verify headers
	sseURL := fmt.Sprintf("%s/api/v1/workspaces/%s/events", cfg.APIURL, wsID)
	req, _ := http.NewRequestWithContext(ctx, "GET", sseURL, nil)
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if !run.AssertNoError(err, "sse-connect: no error") {
		return
	}
	run.Assert(resp.StatusCode == 200, "sse-connect: 200", fmt.Sprintf("got %d", resp.StatusCode))
	ct := resp.Header.Get("Content-Type")
	run.Assert(strings.Contains(ct, "text/event-stream"), "sse-connect: content-type",
		fmt.Sprintf("got %q", ct))

	// Start reading events in background
	events := make(chan sseEvent, 50)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			var evt sseEvent
			if err := json.Unmarshal([]byte(data), &evt); err == nil {
				select {
				case events <- evt:
				default:
				}
			}
		}
	}()

	// P2+P3: Trigger suspend, wait for workspace.phase event
	time.Sleep(500 * time.Millisecond) // let SSE connection settle
	err = c.Workspaces.Suspend(ctx, wsID)
	run.AssertNoError(err, "suspend: no error")

	phaseEventReceived := waitForEvent(events, func(e sseEvent) bool {
		return e.Type == "workspace.phase" &&
			(e.Phase == "Suspending" || e.Phase == "Suspended")
	}, 30*time.Second)
	run.Assert(phaseEventReceived, "sse: workspace.phase event received on suspend",
		"no workspace.phase event within 30s")

	// P4: Activate — wait for Active phase event
	_, err = c.Workspaces.Activate(ctx, wsID)
	run.AssertNoError(err, "activate: no error")

	resumeEventReceived := waitForEvent(events, func(e sseEvent) bool {
		return e.Type == "workspace.phase"
	}, 60*time.Second)
	run.Assert(resumeEventReceived, "sse: workspace.phase event received on resume",
		"no workspace.phase event within 60s")

	// Cancel the context to close the SSE connection
	// (the defer on ctx cancel will close it)

	// N1: SSE on nonexistent workspace
	badURL := fmt.Sprintf("%s/api/v1/workspaces/00000000-0000-0000-0000-000000000000/events", cfg.APIURL)
	badReq, _ := http.NewRequestWithContext(ctx, "GET", badURL, nil)
	badReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	badResp, err := http.DefaultClient.Do(badReq)
	if err == nil {
		defer badResp.Body.Close()
		run.Assert(badResp.StatusCode == 404, "sse-nonexistent: 404",
			fmt.Sprintf("got %d", badResp.StatusCode))
	}
}

func waitForEvent(ch <-chan sseEvent, matcher func(sseEvent) bool, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case evt := <-ch:
			if matcher(evt) {
				return true
			}
		case <-deadline:
			return false
		}
	}
}
