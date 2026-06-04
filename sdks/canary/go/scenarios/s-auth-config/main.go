// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Canary scenario: S-AUTH-CONFIG
// Tests the public GET /auth/config endpoint that all frontend clients call first.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	canary "github.com/lenaxia/llmsafespace/sdks/canary/go"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	run := canary.NewRunner("auth-config", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	runAuthConfig(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("auth-config", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runAuthConfig(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runAuthConfig(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	url := cfg.APIURL + "/api/v1/auth/config"

	// P1: public endpoint — no auth required
	status, body, err := canary.RawDo(ctx, "GET", url, "", nil)
	if !run.AssertNoError(err, "auth/config: reachable") {
		return
	}
	run.Assert(status == 200, fmt.Sprintf("auth/config: 200 (got %d)", status), "")

	var resp map[string]any
	if !run.AssertNoError(json.Unmarshal(body, &resp), "auth/config: valid JSON") {
		return
	}

	// P2–P5: required fields
	_, hasReg := resp["registrationEnabled"]
	run.Assert(hasReg, "auth/config: registrationEnabled present", "")
	_, hasOIDC := resp["oidcEnabled"]
	run.Assert(hasOIDC, "auth/config: oidcEnabled present", "")
	name, _ := resp["instanceName"].(string)
	run.Assert(name != "", "auth/config: instanceName non-empty", fmt.Sprintf("got %q", name))
	_, hasMOTD := resp["motd"]
	run.Assert(hasMOTD, "auth/config: motd field present", "")

	// N1: no error field on success
	run.Assert(!canary.HasField(body, "error"), "auth/config: no error field on success", "")
}
