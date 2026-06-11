package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	llm "github.com/lenaxia/llmsafespace/sdk/go"
	canary "github.com/lenaxia/llmsafespace/sdks/canary/go"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	run := canary.NewRunner("activate-eviction", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 480*time.Second)
	defer cancel()
	runActivateEviction(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("activate-eviction", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 480*time.Second)
	defer cancel()
	runActivateEviction(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runActivateEviction(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	c := cfg.Client()

	maxActive := 3
	if v := os.Getenv("LLMSAFESPACE_MAX_ACTIVE_WORKSPACES_PER_USER"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxActive = n
		}
	}

	var wsIDs []string
	for i := 0; i < maxActive; i++ {
		ws, err := c.Workspaces.Create(ctx, llm.CreateWorkspaceRequest{
			Name: fmt.Sprintf("canary-evict-%d", i), Runtime: "base", StorageSize: "1Gi",
		})
		if !run.AssertNoError(err, fmt.Sprintf("p1-create-%d", i)) {
			return
		}
		wsIDs = append(wsIDs, ws.ID)
		defer func(id string) { _ = c.Workspaces.Delete(context.Background(), id) }(ws.ID)
	}

	allActive := true
	for i, id := range wsIDs {
		phase := canary.WaitActive(ctx, c, id)
		if phase != "Active" {
			run.Assert(false, fmt.Sprintf("p1-active-%d", i), fmt.Sprintf("got %q", phase))
			allActive = false
		}
	}
	if !allActive {
		return
	}
	run.OK("p1-all-active")

	err := c.Workspaces.Suspend(ctx, wsIDs[0])
	run.AssertNoError(err, "p2-suspend-ws0")
	suspPhase := canary.WaitPhase(ctx, c, wsIDs[0], "Suspended", 60*time.Second)
	run.Assert(suspPhase == "Suspended", "p2-ws0-suspended", fmt.Sprintf("got %q", suspPhase))

	wsExtra, err := c.Workspaces.Create(ctx, llm.CreateWorkspaceRequest{
		Name: "canary-evict-extra", Runtime: "base", StorageSize: "1Gi",
	})
	if !run.AssertNoError(err, "p2-create-extra") {
		return
	}
	wsIDs = append(wsIDs, wsExtra.ID)
	defer func() { _ = c.Workspaces.Delete(context.Background(), wsExtra.ID) }()

	extraPhase := canary.WaitActive(ctx, c, wsExtra.ID)
	run.Assert(extraPhase == "Active", "p2-extra-active", fmt.Sprintf("got %q", extraPhase))
	if extraPhase != "Active" {
		return
	}

	resp, err := c.Workspaces.Activate(ctx, wsIDs[0])
	if run.AssertNoError(err, "p2-activate-ws0") {
		run.Assert(resp.Resumed == wsIDs[0], "p3-resumed-is-ws0",
			fmt.Sprintf("got %q", resp.Resumed))

		run.Assert(resp.Suspended != "", "p4-suspended-non-empty", resp.Suspended)
		if resp.Suspended != "" {
			evictPhase := canary.WaitPhaseNot(ctx, c, resp.Suspended, "Active", 60*time.Second)
			run.Assert(evictPhase == "Suspended" || evictPhase == "Suspending", "p5-evicted-transitions",
				fmt.Sprintf("got %q", evictPhase))
		}
	}

	actPhase := canary.WaitActive(ctx, c, wsIDs[0])
	run.Assert(actPhase == "Active", "p6-ws0-active-again", fmt.Sprintf("got %q", actPhase))

	for _, id := range wsIDs {
		_ = c.Workspaces.Delete(context.Background(), id)
	}
	wsIDs = nil

	var n1IDs []string
	for i := 0; i < maxActive; i++ {
		ws, err := c.Workspaces.Create(ctx, llm.CreateWorkspaceRequest{
			Name: fmt.Sprintf("canary-evict-n1-%d", i), Runtime: "base", StorageSize: "1Gi",
		})
		if err != nil {
			break
		}
		n1IDs = append(n1IDs, ws.ID)
		defer func(id string) { _ = c.Workspaces.Delete(context.Background(), id) }(ws.ID)
	}
	for _, id := range n1IDs {
		_ = c.Workspaces.Suspend(ctx, id)
	}
	time.Sleep(2 * time.Second)

	wsN1, err := c.Workspaces.Create(ctx, llm.CreateWorkspaceRequest{
		Name: "canary-evict-n1-sus", Runtime: "base", StorageSize: "1Gi",
	})
	if run.AssertNoError(err, "n1-create-suspended-ws") {
		defer func() { _ = c.Workspaces.Delete(context.Background(), wsN1.ID) }()
		_ = canary.WaitPhase(ctx, c, wsN1.ID, "Suspended", 30*time.Second)
		_ = c.Workspaces.Suspend(ctx, wsN1.ID)
		time.Sleep(2 * time.Second)

		for _, id := range n1IDs {
			_ = c.Workspaces.Restart(ctx, id)
		}
		time.Sleep(1 * time.Second)

		status, body, rawErr := canary.RawDo(ctx, "POST",
			fmt.Sprintf("%s/api/v1/workspaces/%s/activate", cfg.APIURL, wsN1.ID),
			cfg.APIKey, nil)
		if rawErr != nil {
			run.Fail("n1-transitional-cap", rawErr.Error())
		} else {
			run.Assert(status == 409, "n1-transitional-cap: 409",
				fmt.Sprintf("got %d body=%s", status, string(body)[:min(len(body), 200)]))
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
