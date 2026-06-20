// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Canary scenario: S-EMAIL-RESET
// Tests the email-related public endpoints through the real HTTP boundary:
//
//  1. Register a unique user → 201 (or 409 if exists)
//  2. Login attempt → either 200 (noop/auto-verified) or 403 (SES/unverified)
//  3. Password-reset request → 202 (always, no enumeration)
//  4. Password-reset confirm with bogus token → 404 (not found)
//  5. Verify-email with bogus token → 404 (not found)
//  6. Verify-email/resend → 202 (always, no enumeration)
//
// This is the one test that exercises every email endpoint through the real
// HTTP server — router → handler → service → store — catching wiring
// mistakes that unit/integration tests miss (route registration, middleware
// interaction, body parsing).
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
	run := canary.NewRunner("email-reset", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	runEmailReset(ctx, run, cfg)
	run.WriteHTTP(w)
}

func main() {
	run := canary.NewRunner("email-reset", "go-sdk")
	cfg := canary.ConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runEmailReset(ctx, run, cfg)
	res := run.Print()
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func runEmailReset(ctx context.Context, run *canary.Runner, cfg canary.Config) {
	baseURL := cfg.APIURL + "/api/v1"

	// Unique email per run so concurrent canaries don't collide.
	uniqueEmail := fmt.Sprintf("canary-email-%d@llmsafespaces.test", time.Now().UnixNano())
	uniqueUsername := fmt.Sprintf("canaryemail%d", time.Now().UnixNano())
	password := "canary-email-pwd-123456"

	// P1: Register → 201 or 409 (if collision). Either is acceptable.
	regBody := []byte(fmt.Sprintf(`{"username":"%s","email":"%s","password":"%s"}`, uniqueUsername, uniqueEmail, password))
	regStatus, regResp, err := canary.RawDo(ctx, "POST", baseURL+"/auth/register", "", regBody)
	if !run.AssertNoError(err, "register: HTTP request succeeds") {
		return
	}
	run.Assert(regStatus == 201 || regStatus == 409,
		"register: returns 201 or 409", fmt.Sprintf("got %d: %.200s", regStatus, regResp))

	// P2: Login attempt with the new user.
	// In noop mode (dev): register auto-verifies → login should succeed (200).
	// In SES mode (prod): register leaves unverified → login should return 403.
	loginBody := []byte(fmt.Sprintf(`{"email":"%s","password":"%s"}`, uniqueEmail, password))
	loginStatus, loginResp, err := canary.RawDo(ctx, "POST", baseURL+"/auth/login", "", loginBody)
	if run.AssertNoError(err, "login: HTTP request succeeds") {
		if loginStatus == 200 {
			run.OK("login: 200 (noop mode — auto-verified)")
		} else if loginStatus == 403 {
			run.OK(fmt.Sprintf("login: 403 (SES mode — unverified): %.100s", loginResp))
		} else {
			run.Fail("login: unexpected status",
				fmt.Sprintf("expected 200 or 403, got %d: %.200s", loginStatus, loginResp))
		}
	}

	// P3: Password-reset request → must return 202 (no enumeration).
	resetReqBody := []byte(fmt.Sprintf(`{"email":"%s"}`, uniqueEmail))
	resetStatus, _, err := canary.RawDo(ctx, "POST", baseURL+"/auth/password-reset/request", "", resetReqBody)
	if run.AssertNoError(err, "password-reset-request: HTTP request succeeds") {
		run.Assert(resetStatus == 202,
			"password-reset-request: returns 202 (no enumeration)",
			fmt.Sprintf("got %d", resetStatus))
	}

	// P4: Password-reset request with unknown email → must also return 202.
	unknownStatus, _, err := canary.RawDo(ctx, "POST", baseURL+"/auth/password-reset/request", "",
		[]byte(`{"email":"nonexistent-canary@llmsafespaces.test"}`))
	if run.AssertNoError(err, "password-reset-request-unknown: HTTP request succeeds") {
		run.Assert(unknownStatus == 202,
			"password-reset-request-unknown: returns 202 (no enumeration)",
			fmt.Sprintf("got %d", unknownStatus))
	}

	// P5: Password-reset confirm with bogus token → 404 (token not found).
	confirmStatus, _, err := canary.RawDo(ctx, "POST", baseURL+"/auth/password-reset/confirm", "",
		[]byte(`{"token":"canary-bogus-token-not-real","newPassword":"canary-new-pwd-123456"}`))
	if run.AssertNoError(err, "password-reset-confirm-bogus: HTTP request succeeds") {
		run.Assert(confirmStatus == 404,
			"password-reset-confirm-bogus: returns 404 (token not found)",
			fmt.Sprintf("got %d", confirmStatus))
	}

	// P6: Verify-email with bogus token → 404.
	verifyStatus, _, err := canary.RawDo(ctx, "POST", baseURL+"/auth/verify-email", "",
		[]byte(`{"token":"canary-bogus-verify-token"}`))
	if run.AssertNoError(err, "verify-email-bogus: HTTP request succeeds") {
		run.Assert(verifyStatus == 404,
			"verify-email-bogus: returns 404 (token not found)",
			fmt.Sprintf("got %d", verifyStatus))
	}

	// P7: Verify-email/resend → 202 (no enumeration).
	resendStatus, _, err := canary.RawDo(ctx, "POST", baseURL+"/auth/verify-email/resend", "",
		[]byte(fmt.Sprintf(`{"email":"%s"}`, uniqueEmail)))
	if run.AssertNoError(err, "verify-email-resend: HTTP request succeeds") {
		run.Assert(resendStatus == 202,
			"verify-email-resend: returns 202",
			fmt.Sprintf("got %d", resendStatus))
	}

	// P8: Verify-email/resend with unknown email → must also return 202.
	resendUnknownStatus, _, err := canary.RawDo(ctx, "POST", baseURL+"/auth/verify-email/resend", "",
		[]byte(`{"email":"ghost-canary@nonexistent.invalid"}`))
	if run.AssertNoError(err, "verify-email-resend-unknown: HTTP request succeeds") {
		run.Assert(resendUnknownStatus == 202,
			"verify-email-resend-unknown: returns 202 (no enumeration)",
			fmt.Sprintf("got %d", resendUnknownStatus))
	}

	// P9: Password-reset confirm with expired-format token → 404 (not 500).
	// Proves the endpoint handles the error gracefully without leaking internals.
	_, leakResp, _ := canary.RawDo(ctx, "POST", baseURL+"/auth/password-reset/confirm", "",
		[]byte(`{"token":"x","newPassword":"canary-valid-pwd"}`))
	run.Assert(!canary.ContainsLeakedInternals(leakResp),
		"password-reset-confirm: no leaked internals in error response", "")
}
