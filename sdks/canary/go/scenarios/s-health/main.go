// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Canary scenario: S-HEALTH
// Tests /livez, /health, /readyz are reachable, return correct shape,
// and are not rate-limited.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	canary "github.com/lenaxia/llmsafespaces/sdks/canary/go"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	run := canary.NewRunner("health", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	runHealth(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("health", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	runHealth(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runHealth(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	for _, path := range []struct{ name, p string }{
		{"livez", "/livez"},
		{"health-alias", "/health"},
		{"readyz", "/readyz"},
	} {
		status, body, err := canary.RawDo(ctx, "GET", cfg.APIURL+path.p, "", nil)
		if !run.AssertNoError(err, path.name+": reachable") {
			continue
		}
		run.Assert(status == 200, fmt.Sprintf("%s: 200", path.name), fmt.Sprintf("got %d", status))
		run.Assert(canary.HasField(body, "status"), path.name+": has status field", "")
		run.Assert(!canary.ContainsLeakedInternals(body), path.name+": no leaked internals", "")
	}

	// Health endpoints must not be rate-limited: fire 10 rapid requests.
	ok := true
	for i := 0; i < 10; i++ {
		status, _, _ := canary.RawDo(ctx, "GET", cfg.APIURL+"/livez", "", nil)
		if status == 429 {
			ok = false
			break
		}
	}
	run.Assert(ok, "livez: not rate-limited under 10 rapid requests", "got 429")
}
