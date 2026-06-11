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
	run := canary.NewRunner("terminal", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
	defer cancel()
	runTerminal(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("terminal", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	runTerminal(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runTerminal(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	c := cfg.Client()

	ws, err := c.Workspaces.Create(ctx, llm.CreateWorkspaceRequest{
		Name: "canary-terminal", Runtime: "base", StorageSize: "1Gi",
	})
	if !run.AssertNoError(err, "create-ws") {
		return
	}
	wsID := ws.ID
	defer func() { _ = c.Workspaces.Delete(context.Background(), wsID) }()

	phase := canary.WaitActive(ctx, c, wsID)
	run.Assert(phase == "Active", "ws-active", fmt.Sprintf("got %q", phase))
	if phase != "Active" {
		return
	}

	ticket, err := c.Terminal.GetTicket(ctx, wsID)
	if run.AssertNoError(err, "p1-get-ticket") {
		run.Assert(len(ticket.Ticket) > 4 && ticket.Ticket[:4] == "tkt_",
			"p1-ticket-prefix", fmt.Sprintf("got %q", ticket.Ticket))

		run.Assert(len(ticket.Ticket) > 10, "p2-ticket-length",
			fmt.Sprintf("got %d", len(ticket.Ticket)))

		run.Assert(ticket.ExpiresAt != "", "p3-expires-at-non-empty", ticket.ExpiresAt)
	}

	ticket2, err := c.Terminal.GetTicket(ctx, wsID)
	if run.AssertNoError(err, "p4-second-ticket") {
		run.Assert(ticket2.Ticket != ticket.Ticket, "p4-tickets-different",
			fmt.Sprintf("t1=%q t2=%q", ticket.Ticket, ticket2.Ticket))
	}

	_, err = c.Terminal.GetTicket(ctx, "00000000-0000-0000-0000-000000000000")
	run.Assert(err != nil, "n1-nonexistent-ws", canary.ErrDetail(err, "expected error"))

	if cfg.APIKeyUser2 != "" {
		c2 := cfg.Client2()
		_, err = c2.Terminal.GetTicket(ctx, wsID)
		run.Assert(err != nil, "n2-other-user-ws", canary.ErrDetail(err, "expected error"))
	}
}
