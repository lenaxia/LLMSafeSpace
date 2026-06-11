// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Canary scenario: D-KEY-ROTATE
// Tests encryption key rotation.
// Uses canary-rotate@llmsafespace.test account.
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
	run := canary.NewRunner("key-rotate", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
	defer cancel()
	runKeyRotate(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("key-rotate", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	runKeyRotate(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runKeyRotate(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	rotateEmail := os.Getenv("LLMSAFESPACE_ROTATE_EMAIL")
	if rotateEmail == "" {
		rotateEmail = "canary-rotate@llmsafespace.test"
	}
	rotatePassword := os.Getenv("LLMSAFESPACE_ROTATE_PASSWORD")
	if rotatePassword == "" {
		rotatePassword = "canary-rotate-password!"
	}

	rc := llm.New(cfg.APIURL,
		llm.WithCredentials(rotateEmail, rotatePassword),
		llm.WithTimeout(60*time.Second),
	)

	// P1: Create a secret with known value
	secret, err := rc.Secrets.Create(ctx, "canary-rotate-secret", "text", "canary-rotate-value-abc")
	if !run.AssertNoError(err, "create-secret: no error") {
		return
	}
	secretID := secret.ID
	defer func() { _ = rc.Secrets.Delete(context.Background(), secretID) }()

	// P2: RotateKey → response with keyVersion and recoveryKey
	result, err := rc.Account.RotateKey(ctx, rotatePassword)
	if !run.AssertNoError(err, "rotate-key: no error") {
		return
	}
	run.Assert(result["keyVersion"] != nil, "rotate-key: keyVersion present",
		fmt.Sprintf("keyVersion=%v", result["keyVersion"]))

	// P3: recoveryKey is non-empty string
	recoveryKey, _ := result["recoveryKey"].(string)
	run.Assert(recoveryKey != "", "rotate-key: recoveryKey non-empty",
		fmt.Sprintf("recoveryKey=%q", recoveryKey))

	// P4: After rotation, Reveal → correct value (re-encryption succeeded)
	value, err := rc.Secrets.Reveal(ctx, secretID, rotatePassword)
	if run.AssertNoError(err, "reveal-after-rotate: no error") {
		run.Assert(value == "canary-rotate-value-abc", "reveal-after-rotate: correct value",
			fmt.Sprintf("got %q", value))
	}

	// N1: RotateKey with wrong password → error
	_, err = rc.Account.RotateKey(ctx, "wrong-password-xyz")
	run.AssertError(err, "rotate-wrong-password: error")

	// N2: RotateKey with empty password → error
	_, err = rc.Account.RotateKey(ctx, "")
	run.AssertError(err, "rotate-empty-password: error")
}
