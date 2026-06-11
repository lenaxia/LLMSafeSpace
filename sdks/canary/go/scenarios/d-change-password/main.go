// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Canary scenario: D-CHANGE-PASSWORD
// Tests password change flow.
// Uses canary-rotate@llmsafespace.test account.
// Must reset password back to original at end.
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
	run := canary.NewRunner("change-password", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	runChangePassword(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("change-password", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	runChangePassword(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runChangePassword(ctx context.Context, run *canary.Runner, cfg canary.Config) {
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

	// Create a secret for P4 verification
	secret, err := rc.Secrets.Create(ctx, "canary-pwd-secret", "text", "canary-pwd-value")
	if !run.AssertNoError(err, "create-secret: no error") {
		return
	}
	secretID := secret.ID
	defer func() { _ = rc.Secrets.Delete(context.Background(), secretID) }()

	newPassword := "canary-new-pwd-12345678"
	currentPwd := rotatePassword
	defer func() {
		if currentPwd != rotatePassword {
			resetC := llm.New(cfg.APIURL,
				llm.WithCredentials(rotateEmail, currentPwd),
				llm.WithTimeout(60*time.Second),
			)
			_ = resetC.Account.ChangePassword(context.Background(), currentPwd, rotatePassword)
		}
	}()

	// P1: ChangePassword with newPassword ≥ 8 chars → no error
	err = rc.Account.ChangePassword(ctx, rotatePassword, newPassword)
	if !run.AssertNoError(err, "change-password: no error") {
		return
	}
	currentPwd = newPassword

	// P2: Login with new password → succeeds
	rc2 := llm.New(cfg.APIURL,
		llm.WithCredentials(rotateEmail, newPassword),
		llm.WithTimeout(60*time.Second),
	)
	_, err = rc2.Auth.Me(ctx)
	run.AssertNoError(err, "login-new-password: succeeds")

	// P3: Login with old password → 401
	rc3 := llm.New(cfg.APIURL,
		llm.WithCredentials(rotateEmail, rotatePassword),
		llm.WithTimeout(60*time.Second),
	)
	_, err = rc3.Auth.Me(ctx)
	run.Assert(llm.IsAuth(err), "login-old-password: 401",
		fmt.Sprintf("err=%v", err))

	// P4: Reveal with newPassword → correct value
	value, err := rc.Secrets.Reveal(ctx, secretID, newPassword)
	if run.AssertNoError(err, "reveal-new-password: no error") {
		run.Assert(value == "canary-pwd-value", "reveal-new-password: correct value",
			fmt.Sprintf("got %q", value))
	}

	// P5: Change back to original password (idempotency)
	err = rc.Account.ChangePassword(ctx, newPassword, rotatePassword)
	if run.AssertNoError(err, "change-back: no error") {
		currentPwd = rotatePassword
	}

	// N1: Wrong oldPassword → error
	n1c := llm.New(cfg.APIURL,
		llm.WithCredentials(rotateEmail, rotatePassword),
		llm.WithTimeout(60*time.Second),
	)
	err = n1c.Account.ChangePassword(ctx, "wrong-old-password", "newpassword123")
	run.AssertError(err, "n1-wrong-old-password: error")

	// N2: newPassword < 8 chars → error
	err = n1c.Account.ChangePassword(ctx, rotatePassword, "short")
	run.AssertError(err, "n2-short-new-password: error")
}
