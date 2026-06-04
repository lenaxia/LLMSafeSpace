// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Canary scenario: D-WS-LIFECYCLE
// Full workspace lifecycle: create → wait Active → verify status fields →
// suspend (incl. idempotency) → resume (incl. idempotency) → restart → cleanup.
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
	run := canary.NewRunner("ws-lifecycle", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
	defer cancel()
	runWSLifecycle(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("ws-lifecycle", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	runWSLifecycle(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runWSLifecycle(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	c := cfg.Client()

	ws, err := c.Workspaces.Create(ctx, llm.CreateWorkspaceRequest{
		Name: "canary-lifecycle", Runtime: "base", StorageSize: "1Gi",
	})
	if !run.AssertNoError(err, "create: no error") {
		return
	}
	wsID := ws.ID
	defer func() { _ = c.Workspaces.Delete(context.Background(), wsID) }()

	// Wait for Active
	phase := canary.WaitActive(ctx, c, wsID)
	run.Assert(phase == "Active", "reach-active", fmt.Sprintf("got %q", phase))
	if phase != "Active" {
		return
	}

	// P2: Status fields on Active workspace
	status, err := c.Workspaces.GetStatus(ctx, wsID)
	if run.AssertNoError(err, "get-status-active: no error") {
		run.Assert(status.ImageTag != "", "status-active: imageTag non-empty", status.ImageTag)
		run.Assert(status.AgentHealth.AgentVersion != "", "status-active: agentVersion non-empty", status.AgentHealth.AgentVersion)
		run.Assert(len(status.Conditions) > 0, "status-active: conditions non-empty",
			fmt.Sprintf("got %d conditions", len(status.Conditions)))
		run.Assert(status.AgentHealth.Status == "Healthy", "status-active: agentHealth.status=Healthy",
			status.AgentHealth.Status)
		// P3: disk space reported
		run.Assert(status.DiskTotalBytes > 0, "status-active: diskTotalBytes > 0",
			fmt.Sprintf("got %d", status.DiskTotalBytes))
		// P5: CredentialsAvailable condition present
		foundCredCond := false
		for _, c := range status.Conditions {
			if c.Type == "CredentialsAvailable" {
				foundCredCond = true
				break
			}
		}
		run.Assert(foundCredCond, "status-active: CredentialsAvailable condition present", "")
	}

	// P6: Suspend
	err = c.Workspaces.Suspend(ctx, wsID)
	run.AssertNoError(err, "suspend: no error")

	suspPhase := canary.WaitPhase(ctx, c, wsID, "Suspended", 60*time.Second)
	run.Assert(suspPhase == "Suspended", "suspend: phase=Suspended",
		fmt.Sprintf("got %q", suspPhase))

	// P7: Suspend already-Suspended → 409 Conflict
	err = c.Workspaces.Suspend(ctx, wsID)
	run.Assert(err != nil && llm.IsConflict(err),
		"double-suspend: 409 Conflict",
		canary.ErrDetail(err, "expected IsConflict=true"))

	// P8: Resume
	err = c.Workspaces.Resume(ctx, wsID)
	run.AssertNoError(err, "resume: no error")

	resumePhase := canary.WaitActive(ctx, c, wsID)
	run.Assert(resumePhase == "Active", "resume: phase=Active",
		fmt.Sprintf("got %q", resumePhase))

	// P9: Resume already-Active → idempotent (202/200, no error)
	err = c.Workspaces.Resume(ctx, wsID)
	run.AssertNoError(err, "resume-already-active: idempotent no error")

	// P10: Restart
	err = c.Workspaces.Restart(ctx, wsID)
	run.AssertNoError(err, "restart: no error")

	restartPhase := canary.WaitActive(ctx, c, wsID)
	run.Assert(restartPhase == "Active", "restart: returns to Active",
		fmt.Sprintf("got %q", restartPhase))

	// N3: Restart Terminating — delete first then restart the (now-deleted) ID
	_ = c.Workspaces.Delete(ctx, wsID)
	time.Sleep(2 * time.Second)
	err = c.Workspaces.Restart(ctx, wsID)
	// Should fail — workspace is terminating/terminated/deleted
	run.Assert(err != nil, "restart-terminated: error", canary.ErrDetail(err, "expected error"))
}
