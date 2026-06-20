// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func validCloudInitConfig() CloudInitConfig {
	return CloudInitConfig{
		UpstreamURL: "https://opencode.ai/zen/v1",
		Token:       "test-token-abc123",
		ArtifactURLs: []string{
			"https://github.com/lenaxia/llmsafespace/releases/latest/download",
			"https://s3.amazonaws.com/llmsafespace-artifacts",
		},
		ArtifactSHA256: "abc123def456",
	}
}

func TestRenderCloudInit_ValidConfig(t *testing.T) {
	b64, err := RenderCloudInit(validCloudInitConfig())
	require.NoError(t, err)

	raw, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err)
	content := string(raw)

	assert.Contains(t, content, "#cloud-config")
	assert.Contains(t, content, "relay-proxy.service")
	assert.Contains(t, content, "--upstream=https://opencode.ai/zen/v1")
	assert.Contains(t, content, "--listen=0.0.0.0:8080",
		"cloud-init must render --listen=0.0.0.0:8080 so the proxy binds all interfaces (token-gated, not network-isolated)")
	assert.Contains(t, content, "--token=test-token-abc123",
		"cloud-init must pass the per-VM token so the proxy gates on it")
	assert.NotContains(t, content, "wireguard",
		"cloud-init must NOT install or configure WireGuard (removed in worklog 0442)")
	assert.NotContains(t, content, "wg-quick",
		"cloud-init must NOT bring up any WG interface (removed in worklog 0442)")
}

func TestRenderCloudInit_MissingUpstreamURL(t *testing.T) {
	cfg := validCloudInitConfig()
	cfg.UpstreamURL = ""
	_, err := RenderCloudInit(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "upstream URL")
}

func TestRenderCloudInit_MissingToken(t *testing.T) {
	cfg := validCloudInitConfig()
	cfg.Token = ""
	_, err := RenderCloudInit(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "token",
		"missing token must be rejected — without it the proxy is an open forwarder to the upstream")
}

func TestRenderCloudInit_MissingArtifactURLs(t *testing.T) {
	cfg := validCloudInitConfig()
	cfg.ArtifactURLs = nil
	_, err := RenderCloudInit(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "artifact URL",
		"without an artifact URL the relay-proxy binary can never reach the VM — cloud-init would start a nonexistent binary")
}

func TestRenderCloudInit_MissingArtifactSHA256(t *testing.T) {
	cfg := validCloudInitConfig()
	cfg.ArtifactSHA256 = ""
	_, err := RenderCloudInit(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "artifact SHA-256",
		"without a checksum the download cannot be integrity-verified — security doc §7 mandates sha256sum -c before exec")
}

func TestRenderCloudInit_RendersArtifactDownload(t *testing.T) {
	b64, err := RenderCloudInit(validCloudInitConfig())
	require.NoError(t, err)

	raw, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err)
	content := string(raw)

	assert.Contains(t, content, "curl",
		"curl must be installed to download the relay-proxy binary from the artifact mirror")
	assert.Contains(t, content, "sha256sum",
		"downloaded binary must be SHA-256 verified before exec (security doc §7)")
	assert.Contains(t, content, "abc123def456",
		"the embedded checksum must appear in the rendered cloud-init")
	assert.Contains(t, content, "https://github.com/lenaxia/llmsafespace/releases/latest/download",
		"the first mirror URL must appear in the rendered cloud-init")
	assert.Contains(t, content, "https://s3.amazonaws.com/llmsafespace-artifacts",
		"the second mirror URL must appear in the rendered cloud-init (multi-mirror fallback)")
	assert.Contains(t, content, "relay-proxy-arm64",
		"cloud-init must resolve the arm64 binary name (AWS t4g.micro / OCI A1 are arm64)")
	assert.Contains(t, content, "/usr/local/bin/relay-proxy",
		"binary must land at the path the systemd unit references")
	assert.Contains(t, content, "FATAL",
		"download failure must be fatal (set -e semantics / explicit exit) so the VM does not silently run without the proxy")
}

func TestRenderCloudInit_DownloadBeforeSystemdStart(t *testing.T) {
	b64, err := RenderCloudInit(validCloudInitConfig())
	require.NoError(t, err)

	raw, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err)
	content := string(raw)

	downloadIdx := strings.Index(content, "/usr/local/bin/relay-proxy") // first occurrence is the download target
	startIdx := strings.Index(content, "systemctl start relay-proxy")
	assert.Greater(t, downloadIdx, -1)
	assert.Greater(t, startIdx, -1)
	assert.Less(t, downloadIdx, startIdx,
		"the binary download+verify must run BEFORE systemctl start relay-proxy, or systemd will fail with file-not-found")
}

func TestParseHealthMetrics_BasicMetrics(t *testing.T) {
	raw := `# HELP relay_router_relay_healthy Relay health
relay_router_relay_healthy{id="oci-1",provider="oci"} 1
relay_router_relay_healthy{id="aws-1",provider="aws"} 0
relay_router_active_streams{id="oci-1",provider="oci"} 3
relay_router_requests_total{id="oci-1",provider="oci"} 12847
relay_router_requests_429_total{id="oci-1",provider="oci"} 2
relay_router_relay_egress_bytes{id="oci-1",provider="oci"} 149546362
relay_router_fallback_active 0
`
	report := parseHealthMetrics(raw)

	assert.False(t, report.FallbackActive)
	require.Contains(t, report.Relays, "oci-1")
	assert.True(t, report.Relays["oci-1"].Healthy)
	assert.Equal(t, int64(3), report.Relays["oci-1"].ActiveStreams)
	assert.Equal(t, int64(12847), report.Relays["oci-1"].Requests)
	assert.Equal(t, int64(2), report.Relays["oci-1"].Requests429)
	assert.Equal(t, int64(149546362), report.Relays["oci-1"].EgressBytes)

	require.Contains(t, report.Relays, "aws-1")
	assert.False(t, report.Relays["aws-1"].Healthy)
}

func TestParseHealthMetrics_FallbackActive(t *testing.T) {
	raw := `relay_router_fallback_active 1`
	report := parseHealthMetrics(raw)
	assert.True(t, report.FallbackActive)
}

func TestParseHealthMetrics_EmptyInput(t *testing.T) {
	report := parseHealthMetrics("")
	assert.False(t, report.FallbackActive)
	assert.Empty(t, report.Relays)
}

func TestParseHealthMetrics_SkipsCommentsAndEmpty(t *testing.T) {
	raw := `# HELP some_metric Help text
# TYPE some_metric counter

some_other_metric 42
`
	report := parseHealthMetrics(raw)
	assert.Empty(t, report.Relays)
}

func TestDefaultShapeForProvider(t *testing.T) {
	assert.Equal(t, "t4g.micro", defaultShapeForProvider("aws"))
	assert.Equal(t, "VM.Standard.A1.Flex", defaultShapeForProvider("oci"))
	assert.Equal(t, "e2-micro", defaultShapeForProvider("gcp"))
}

func TestDefaultRegionForProvider(t *testing.T) {
	assert.Equal(t, "us-east-1", defaultRegionForProvider("aws"))
	assert.Equal(t, "us-ashburn-1", defaultRegionForProvider("oci"))
	assert.Equal(t, "us-west1", defaultRegionForProvider("gcp"))
}

func TestErrorClassification(t *testing.T) {
	capErr := fmt.Errorf("wrap: %w", ErrCapacity)
	assert.True(t, IsCapacityError(capErr))
	assert.False(t, IsConfigError(capErr))

	cfgErr := fmt.Errorf("wrap: %w", ErrConfig)
	assert.True(t, IsConfigError(cfgErr))
	assert.False(t, IsCapacityError(cfgErr))
}

func TestAWSDriver_ConstructsWithCorrectSecret(t *testing.T) {
	d := NewAWSDriver(nil, "test-ns", "aws-relay-irwa")
	assert.Equal(t, "aws-relay-irwa", d.credentialSecret)
	assert.Equal(t, "test-ns", d.namespace)
}

func TestOCIDriver_GetConfig_MissingSecret(t *testing.T) {
	fakeClient := fake.NewClientBuilder().Build()
	d := NewOCIDriver(fakeClient, "test", "oci-credentials")
	_, err := d.getConfig(context.Background(), "missing")
	assert.Error(t, err)
}

func TestOCIStateToVMState(t *testing.T) {
	assert.Equal(t, VMStateRunning, ociStateToVMState("RUNNING"))
	assert.Equal(t, VMStatePending, ociStateToVMState("PROVISIONING"))
	assert.Equal(t, VMStateStopped, ociStateToVMState("STOPPED"))
	assert.Equal(t, VMStateTerminated, ociStateToVMState("TERMINATED"))
	assert.Equal(t, VMStatePending, ociStateToVMState("UNKNOWN"))
}

func TestClassifyOCIError(t *testing.T) {
	assert.True(t, IsCapacityError(classifyOCIError(500, "internal error")))
	assert.True(t, IsCapacityError(classifyOCIError(503, "unavailable")))
	assert.True(t, IsCapacityError(classifyOCIError(429, "rate limited")))
	assert.True(t, IsConfigError(classifyOCIError(400, "bad request")))
	assert.True(t, IsConfigError(classifyOCIError(401, "unauthorized")))
}

func TestParseRSAPrivateKeyPEM_InvalidPEM(t *testing.T) {
	_, err := parseRSAPrivateKeyPEM("not a key")
	assert.Error(t, err)
}

func TestIndentLines(t *testing.T) {
	result := indentLines(4, "line1\nline2\n")
	assert.Equal(t, "    line1\n    line2\n", result)
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 10))
	assert.Equal(t, "hello...", truncate("hello world", 5))
}
