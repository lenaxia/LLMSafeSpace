// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	llm "github.com/lenaxia/llmsafespaces/sdk/go"
	canary "github.com/lenaxia/llmsafespaces/sdks/canary/go"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	run := canary.NewRunner("model-list-annotated", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
	defer cancel()
	runModelListAnnotated(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("model-list-annotated", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	runModelListAnnotated(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runModelListAnnotated(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	if cfg.LLMAPIKey == "" {
		run.OK("skipped: no LLM key")
		return
	}

	c := cfg.Client()

	ws, err := c.Workspaces.Create(ctx, llm.CreateWorkspaceRequest{
		Name: "canary-model-list", Runtime: "base", StorageSize: "1Gi",
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

	models, err := c.Workspaces.GetModels(ctx, wsID)
	if run.AssertNoError(err, "p1-get-models") {
		run.Assert(len(models.Models) > 0, "p1-models-non-empty",
			fmt.Sprintf("got %d", len(models.Models)))
		run.Assert(models.CurrentModel != "", "p1-current-model-non-empty",
			models.CurrentModel)

		for i, m := range models.Models {
			run.Assert(m.ID != "", fmt.Sprintf("p2-model-%d-id", i), m.ID)
			run.Assert(m.Name != "", fmt.Sprintf("p2-model-%d-name", i), m.Name)
			run.Assert(m.Tier == "free" || m.Tier == "paid",
				fmt.Sprintf("p2-model-%d-tier", i), fmt.Sprintf("got %q", m.Tier))
		}

		selectedCount := 0
		var selectedID string
		for _, m := range models.Models {
			if m.Selected {
				selectedCount++
				selectedID = m.ID
			}
		}
		run.Assert(selectedCount == 1, "p3-exactly-one-selected",
			fmt.Sprintf("got %d", selectedCount))
		run.Assert(selectedID == models.CurrentModel, "p3-selected-equals-current",
			fmt.Sprintf("selected=%q current=%q", selectedID, models.CurrentModel))
	}

	if len(models.Models) >= 2 {
		var otherModel string
		for _, m := range models.Models {
			if m.ID != models.CurrentModel {
				otherModel = m.ID
				break
			}
		}
		if otherModel != "" {
			err = c.Workspaces.SetModel(ctx, wsID, otherModel)
			if run.AssertNoError(err, "p4-set-model") {
				updated, err := c.Workspaces.GetModels(ctx, wsID)
				if run.AssertNoError(err, "p4-get-models-after") {
					run.Assert(updated.CurrentModel == otherModel, "p4-new-current",
						fmt.Sprintf("got %q want %q", updated.CurrentModel, otherModel))
					found := false
					for _, m := range updated.Models {
						if m.ID == otherModel {
							run.Assert(m.Selected, "p4-new-selected",
								fmt.Sprintf("model %q selected=%v", m.ID, m.Selected))
							found = true
						}
					}
					run.Assert(found, "p4-other-model-in-list", otherModel)
				}
			}
		}
	}

	_, err = c.Workspaces.GetModels(ctx, "00000000-0000-0000-0000-000000000000")
	run.Assert(err != nil, "n1-nonexistent-ws", canary.ErrDetail(err, "expected error"))
}
