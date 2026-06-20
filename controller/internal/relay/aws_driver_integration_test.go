// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration

// This file is excluded from normal builds (`go test` without -tags=integration).
// It exercises the real AWS driver against live EC2 and incurs a small charge
// (~$0.01 per run). Requires AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY in env.
//
// Run with:
//   AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... \
//     go test -tags=integration -timeout 10m -v -run TestIntegration_AWS ./controller/internal/relay/

package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestIntegration_AWSDriver_ProvisionCloudInit_Completion is the full E2E:
// AWSDriver.Provision → real EC2 → cloud-init downloads relay-proxy →
// relay-proxy starts with token → free-model completion through the proxy →
// AWSDriver.Destroy. Closes the 0% coverage gap on aws_driver.go and
// validates the cloud-init → token-auth → Zen path end-to-end.
func TestIntegration_AWSDriver_ProvisionCloudInit_Completion(t *testing.T) {
	ctx := context.Background()

	cloudInit, err := RenderCloudInit(CloudInitConfig{
		UpstreamURL: "https://opencode.ai/zen/v1",
		Token:       "e2e-test-token-secret-xyz",
		ArtifactURLs: []string{
			"https://github.com/lenaxia/LLMSafeSpaces/releases/download/v0.1.0-relay",
		},
		ArtifactSHA256: "671c46c6c3c1b0afabe9fcdf4c815f4c0e08fe2c28d5d6eff988ba20900b2fc8",
		BinaryName:     "relay-proxy-arm64",
	})
	require.NoError(t, err)

	// Fake K8s client with no Secret → loadAWSConfig falls back to env creds.
	scheme := runtime.NewScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	driver := NewAWSDriver(fakeClient, "test-ns", "aws-relay-irwa")

	t.Log("Provisioning EC2 via AWSDriver.Provision()...")
	start := time.Now()
	result, err := driver.Provision(ctx, ProvisionRequest{
		Name:      "relay-e2e-test",
		Region:    "us-east-1",
		Shape:     "t4g.micro",
		CloudInit: cloudInit,
	})
	require.NoError(t, err, "Provision must succeed against real EC2")
	require.NotEmpty(t, result.InstanceID)
	require.NotEmpty(t, result.PublicIP)
	t.Logf("Provisioned %s @ %s in %s", result.InstanceID, result.PublicIP, time.Since(start))

	t.Cleanup(func() {
		t.Logf("Destroying %s...", result.InstanceID)
		dCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := driver.Destroy(dCtx, result.InstanceID, "us-east-1"); err != nil {
			t.Errorf("Destroy failed: %v", err)
		}
	})

	// Wait for cloud-init to finish downloading + starting relay-proxy.
	endpoint := fmt.Sprintf("%s:8080", result.PublicIP)
	t.Logf("Waiting for relay-proxy healthz at http://%s/healthz...", endpoint)
	require.True(t, waitForHealthz(t, ctx, endpoint, 3*time.Minute),
		"relay-proxy must be reachable within 3 minutes")

	// Free-model completion through the relay-proxy WITH token.
	t.Log("Completion call through relay-proxy...")
	completionURL := fmt.Sprintf("http://%s/chat/completions", endpoint)
	body := strings.NewReader(`{"model":"deepseek-v4-flash-free","messages":[{"role":"user","content":"What is 2+2? Just the number."}],"max_tokens":50}`)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, completionURL, body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer public")
	req.Header.Set("X-Relay-Token", "e2e-test-token-secret-xyz")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "free-model completion must succeed")
	respBody, _ := io.ReadAll(resp.Body)
	t.Logf("Completion: %s", truncateStr(string(respBody), 200))

	// Token rejection: no X-Relay-Token → 401.
	t.Log("Verifying token rejection...")
	body2 := strings.NewReader(`{"model":"deepseek-v4-flash-free","messages":[{"role":"user","content":"hi"}],"max_tokens":5}`)
	req2, _ := http.NewRequestWithContext(ctx, http.MethodPost, completionURL, body2)
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer public")
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer func() { _ = resp2.Body.Close() }()
	assert.Equal(t, http.StatusUnauthorized, resp2.StatusCode, "missing token must 401")
}

func waitForHealthz(t *testing.T, ctx context.Context, endpoint string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 5 * time.Second}
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+endpoint+"/healthz", nil)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(5 * time.Second)
	}
	return false
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
