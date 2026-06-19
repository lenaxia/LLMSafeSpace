// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Canary scenario: D-ACCOUNT-RECOVER
// Tests account recovery with recovery key.
// Uses canary-rotate@llmsafespaces.test account.
// Resets password to original at end.
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
	run := canary.NewRunner("account-recover", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	runAccountRecover(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("account-recover", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	runAccountRecover(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runAccountRecover(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	rotateEmail := os.Getenv("LLMSAFESPACES_ROTATE_EMAIL")
	if rotateEmail == "" {
		rotateEmail = "canary-rotate@llmsafespaces.test"
	}
	rotatePassword := os.Getenv("LLMSAFESPACES_ROTATE_PASSWORD")
	if rotatePassword == "" {
		rotatePassword = "canary-rotate-password!"
	}

	rc := llm.New(cfg.APIURL,
		llm.WithCredentials(rotateEmail, rotatePassword),
		llm.WithTimeout(60*time.Second),
	)

	// Get userID for recover
	me, err := rc.Auth.Me(ctx)
	if !run.AssertNoError(err, "login-rotate: no error") {
		return
	}
	userID, _ := me["id"].(string)
	run.Assert(userID != "", "get-userid: non-empty", "")

	// Create a secret for P4 verification
	secret, err := rc.Secrets.Create(ctx, "canary-recover-secret", "text", "canary-recover-value")
	if !run.AssertNoError(err, "create-secret: no error") {
		return
	}
	secretID := secret.ID
	defer func() { _ = rc.Secrets.Delete(context.Background(), secretID) }()

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

	// P1: RotateKey to get fresh recoveryKey
	rotateResult, err := rc.Account.RotateKey(ctx, rotatePassword)
	if !run.AssertNoError(err, "rotate-key: no error") {
		return
	}
	recoveryKey, _ := rotateResult["recoveryKey"].(string)
	run.Assert(recoveryKey != "", "rotate-key: recoveryKey non-empty",
		fmt.Sprintf("recoveryKey=%q", recoveryKey))

	newPassword := "canary-recover-new-pwd123"

	// P2: Recover → response with new recoveryKey
	recoverResult, err := rc.Account.Recover(ctx, userID, recoveryKey, newPassword)
	if run.AssertNoError(err, "recover: no error") {
		newRecoveryKey, _ := recoverResult["recoveryKey"].(string)
		run.Assert(newRecoveryKey != "", "recover: new recoveryKey non-empty",
			fmt.Sprintf("recoveryKey=%q", newRecoveryKey))
	}
	currentPwd = newPassword

	// P3: Login with newPassword → succeeds
	rc2 := llm.New(cfg.APIURL,
		llm.WithCredentials(rotateEmail, newPassword),
		llm.WithTimeout(60*time.Second),
	)
	_, err = rc2.Auth.Me(ctx)
	run.AssertNoError(err, "login-new-password: succeeds")

	// P4: Reveal with newPassword → correct value
	value, err := rc2.Secrets.Reveal(ctx, secretID, newPassword)
	if run.AssertNoError(err, "reveal-after-recover: no error") {
		run.Assert(value == "canary-recover-value", "reveal-after-recover: correct value",
			fmt.Sprintf("got %q", value))
	}

	// Reset password to original for cleanup
	err = rc2.Account.ChangePassword(ctx, newPassword, rotatePassword)
	if err == nil {
		currentPwd = rotatePassword
	}

	// N1: Recover with invalid recovery key → error
	_, err = rc.Account.Recover(ctx, userID, "invalid-recovery-key-xyz", "newpwd12345")
	run.AssertError(err, "n1-invalid-recovery-key: error")

	// N2: Recover with missing fields → error
	status, _, _ := canary.RawDo(ctx, "POST",
		fmt.Sprintf("%s/api/v1/account/recover", cfg.APIURL),
		cfg.APIKey, []byte(`{"userId":"`+userID+`"}`))
	run.Assert(status == 400, "n2-missing-fields: 400", fmt.Sprintf("got %d", status))
}
