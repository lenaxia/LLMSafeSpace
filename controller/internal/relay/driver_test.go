// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestRenderCloudInit_ValidConfig(t *testing.T) {
	b64, err := RenderCloudInit(CloudInitConfig{
		WgConfig:       "[Interface]\nPrivateKey = test123\n",
		UpstreamURL:    "https://opencode.ai/zen/v1",
		RouterEndpoint: "relay-gw.example.com:51820",
	})
	require.NoError(t, err)

	raw, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err)
	content := string(raw)

	assert.Contains(t, content, "#cloud-config")
	assert.Contains(t, content, "wireguard")
	assert.Contains(t, content, "wg0.conf")
	assert.Contains(t, content, "relay-proxy.service")
	assert.Contains(t, content, "--upstream=https://opencode.ai/zen/v1")
	assert.Contains(t, content, "wg-quick@wg0")
}

func TestRenderCloudInit_MissingWGConfig(t *testing.T) {
	_, err := RenderCloudInit(CloudInitConfig{
		UpstreamURL: "https://example.com",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "WireGuard config")
}

func TestRenderCloudInit_MissingUpstreamURL(t *testing.T) {
	_, err := RenderCloudInit(CloudInitConfig{
		WgConfig: "[Interface]\nPrivateKey = x\n",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "upstream URL")
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

func TestWgIPForProvider(t *testing.T) {
	assert.Equal(t, wgAWSRelay, wgIPForProvider("aws"))
	assert.Equal(t, wgOCIRelay, wgIPForProvider("oci"))
	assert.Equal(t, wgGCPRelay, wgIPForProvider("gcp"))
	assert.Equal(t, "", wgIPForProvider("unknown"))
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

func TestAWSDriver_NotImplemented(t *testing.T) {
	d := &AWSDriver{}
	_, err := d.Provision(context.Background(), ProvisionRequest{})
	assert.ErrorIs(t, err, ErrNotImplemented)

	err = d.Destroy(context.Background(), "i-123", "us-east-1")
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestGCPDriver_NotImplemented(t *testing.T) {
	d := &GCPDriver{}
	_, err := d.Provision(context.Background(), ProvisionRequest{})
	assert.ErrorIs(t, err, ErrNotImplemented)
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
