// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// seed-accounts creates the two canary test accounts (canary1 and canary2) needed
// by the SDK canary CI job. It is idempotent: if an account already exists (409
// or login succeeds), it continues without error.
//
// Usage:
//
//	go run ./sdks/canary/go/cmd/seed-accounts/ \
//	    --url http://localhost:8080 \
//	    --out /tmp/canary-keys.env
//
// Output format (sourced by CI):
//
//	CANARY_API_KEY_1=lsp_...
//	CANARY_API_KEY_2=lsp_...
//	CANARY_PASSWORD_1=<password>
//	CANARY_PASSWORD_2=<password>
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
)

var (
	flagURL = flag.String("url", "http://localhost:8080", "API base URL")
	flagOut = flag.String("out", "", "Output file for env vars (defaults to stdout)")
)

type account struct {
	username string
	email    string
	password string
}

var canaryAccounts = []account{
	{"canary1", "canary1@llmsafespaces.test", "canary-password-1-abc123!"},
	{"canary2", "canary2@llmsafespaces.test", "canary-password-2-xyz456!"},
}

func main() {
	flag.Parse()

	keys := make([]string, len(canaryAccounts))
	for i, a := range canaryAccounts {
		key, err := ensureAccount(*flagURL, a)
		if err != nil {
			fmt.Fprintf(os.Stderr, "seed-accounts: account %s: %v\n", a.email, err)
			os.Exit(1)
		}
		keys[i] = key
	}

	var out io.Writer = os.Stdout
	if *flagOut != "" {
		f, err := os.Create(*flagOut)
		if err != nil {
			fmt.Fprintf(os.Stderr, "seed-accounts: create output file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
	}

	fmt.Fprintf(out, "CANARY_API_KEY_1=%s\n", keys[0])
	fmt.Fprintf(out, "CANARY_API_KEY_2=%s\n", keys[1])
	fmt.Fprintf(out, "CANARY_PASSWORD_1=%s\n", canaryAccounts[0].password)
	fmt.Fprintf(out, "CANARY_PASSWORD_2=%s\n", canaryAccounts[1].password)
}

func ensureAccount(baseURL string, a account) (string, error) {
	registerBody, _ := json.Marshal(map[string]string{
		"username": a.username,
		"email":    a.email,
		"password": a.password,
	})
	regResp, err := http.Post(baseURL+"/api/v1/auth/register", "application/json", bytes.NewReader(registerBody))
	if err != nil {
		return "", fmt.Errorf("register request: %w", err)
	}
	regResp.Body.Close()

	if regResp.StatusCode != 201 && regResp.StatusCode != 409 {
		return "", fmt.Errorf("register returned %d for %s", regResp.StatusCode, a.email)
	}

	loginBody, _ := json.Marshal(map[string]string{"email": a.email, "password": a.password})
	loginResp, err := http.Post(baseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		return "", fmt.Errorf("login request: %w", err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode != 200 {
		return "", fmt.Errorf("login returned %d for %s", loginResp.StatusCode, a.email)
	}
	var loginResult struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&loginResult); err != nil {
		return "", fmt.Errorf("decode login: %w", err)
	}

	keyBody, _ := json.Marshal(map[string]string{"name": "canary-ci"})
	keyReq, _ := http.NewRequest("POST", baseURL+"/api/v1/auth/api-keys", bytes.NewReader(keyBody))
	keyReq.Header.Set("Content-Type", "application/json")
	keyReq.Header.Set("Authorization", "Bearer "+loginResult.Token)
	keyResp, err := http.DefaultClient.Do(keyReq)
	if err != nil {
		return "", fmt.Errorf("create api key: %w", err)
	}
	defer keyResp.Body.Close()
	if keyResp.StatusCode != 201 {
		return "", fmt.Errorf("create api key returned %d for %s", keyResp.StatusCode, a.email)
	}
	var keyResult struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(keyResp.Body).Decode(&keyResult); err != nil {
		return "", fmt.Errorf("decode api key: %w", err)
	}
	if keyResult.Key == "" {
		return "", fmt.Errorf("empty api key for %s", a.email)
	}
	return keyResult.Key, nil
}
