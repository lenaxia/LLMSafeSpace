// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// TestEndpointForInstance_BuildsHostPort verifies the endpoint the router
// dials is "<publicIP>:8080" — matching relay-proxy's --listen=0.0.0.0:8080
// default (worklog 0442).
func TestEndpointForInstance_BuildsHostPort(t *testing.T) {
	inst := v1.RelayInstanceStatus{PublicIP: "203.0.113.42"}
	assert.Equal(t, "203.0.113.42:8080", endpointForInstance(inst))
}

// TestEndpointForInstance_EmptyPublicIPReturnsEmpty verifies the empty case —
// a not-yet-provisioned instance must produce no endpoint so the router skips
// it rather than dialing ":8080" (which would hit localhost).
func TestEndpointForInstance_EmptyPublicIPReturnsEmpty(t *testing.T) {
	inst := v1.RelayInstanceStatus{}
	assert.Equal(t, "", endpointForInstance(inst))
}

// TestRenderCloudInit_NoWireGuardArtifacts is the regression guard for the WG
// removal (worklog 0442). The rendered cloud-init must contain ZERO WG
// artifacts — no packages, no wg0.conf writefile, no wg-quick systemctl. A
// future edit that re-introduces WG would fail this test loudly.
func TestRenderCloudInit_NoWireGuardArtifacts(t *testing.T) {
	b64, err := RenderCloudInit(validCloudInitConfig())
	require.NoError(t, err)

	raw, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err)
	content := string(raw)

	for _, bad := range []string{"wireguard", "wg-quick", "wg0.conf", "51820", "10.42.42"} {
		assert.NotContains(t, content, bad,
			"cloud-init must NOT contain WG artifact %q (removed in worklog 0442)", bad)
	}
}

// TestSyncPeerConfigMap_ContainsEndpointAndToken verifies the wire format of
// peers.json matches the relay-router's PeerEntry struct: it must contain
// `endpoint` and `token`, and must NOT contain the removed `wgIP`/`publicKey`
// fields. JSON tag drift between the two PeerEntry definitions would break
// routing silently; this test catches it.
func TestSyncPeerConfigMap_ContainsEndpointAndToken(t *testing.T) {
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	peers := []PeerEntry{
		{ID: "aws-1", Endpoint: "203.0.113.10:8080", Provider: "aws", State: "healthy", Token: "tok-aws-xyz"},
	}
	require.NoError(t, syncPeerConfigMap(context.Background(), fakeClient, "test-ns", peers))

	cm := &corev1.ConfigMap{}
	require.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{Name: routerPeersConfigMap, Namespace: "test-ns"}, cm))

	raw := cm.Data["peers.json"]
	assert.Contains(t, raw, `"endpoint":"203.0.113.10:8080"`, "peers.json must carry the public-IP endpoint")
	assert.Contains(t, raw, `"token":"tok-aws-xyz"`, "peers.json must carry the per-VM token")
	assert.NotContains(t, raw, "wgIP", "peers.json must NOT carry the removed wgIP field")
	assert.NotContains(t, raw, "publicKey", "peers.json must NOT carry the removed publicKey field")

	// Verify it round-trips through the router's ParsePeerConfig shape (the
	// JSON tags must match exactly — extra/missing fields would silently
	// drop data).
	var parsed struct {
		Relays []map[string]any `json:"relays"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &parsed))
	require.Len(t, parsed.Relays, 1)
	assert.Equal(t, "203.0.113.10:8080", parsed.Relays[0]["endpoint"])
	assert.Equal(t, "tok-aws-xyz", parsed.Relays[0]["token"])
}

// TestProvisionRelay_ReusesExistingToken verifies the persistence path: when
// the reconciler has a previously-stored token for a provider slot, it MUST
// pass that same token to the new VM (a fresh token would 401 at the router
// until the VM is destroyed + recreated). This guards against a controller
// restart causing a silent desync between running VMs and peers.json.
func TestProvisionRelay_ReusesExistingToken(t *testing.T) {
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(makeOCISecret()).
		Build()

	r := &InferenceRelayReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		Namespace: "test-ns",
		Drivers:   map[string]ProviderDriver{"oci": &stubDriver{provisionResult: &ProvisionResult{InstanceID: "i-new", PublicIP: "1.2.3.4"}}},
		ArtifactURLs: []string{
			"https://github.com/lenaxia/llmsafespace/releases/latest/download",
		},
		ArtifactSHA256Arm64: strings.Repeat("a", 64),
		ArtifactSHA256Amd64: strings.Repeat("b", 64),
	}

	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet"},
		Spec: v1.InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers: []v1.RelayProviderSpec{
				{Provider: "oci", Region: "us-ashburn-1", CredentialsRef: corev1.LocalObjectReference{Name: "oci-credentials"}},
			},
		},
	}

	existing := "previously-persisted-token-abc"
	_, token, err := r.provisionRelay(context.Background(), relay, relay.Spec.Providers[0], existing)
	require.NoError(t, err)
	assert.Equal(t, existing, token,
		"provisionRelay must reuse the existing token — a fresh token would 401 at the router until the VM is destroyed + recreated")
}

// TestProvisionRelay_GeneratesFreshTokenWhenNoneExists is the complementary
// path: when no existing token is passed (first provision of a slot), the
// reconciler must generate a new one.
func TestProvisionRelay_GeneratesFreshTokenWhenNoneExists(t *testing.T) {
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(makeOCISecret()).
		Build()

	r := &InferenceRelayReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		Namespace: "test-ns",
		Drivers:   map[string]ProviderDriver{"oci": &stubDriver{provisionResult: &ProvisionResult{InstanceID: "i-new", PublicIP: "1.2.3.4"}}},
		ArtifactURLs: []string{
			"https://github.com/lenaxia/llmsafespace/releases/latest/download",
		},
		ArtifactSHA256Arm64: strings.Repeat("a", 64),
		ArtifactSHA256Amd64: strings.Repeat("b", 64),
	}

	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet"},
		Spec: v1.InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers: []v1.RelayProviderSpec{
				{Provider: "oci", Region: "us-ashburn-1", CredentialsRef: corev1.LocalObjectReference{Name: "oci-credentials"}},
			},
		},
	}

	_, token, err := r.provisionRelay(context.Background(), relay, relay.Spec.Providers[0], "")
	require.NoError(t, err)
	assert.NotEmpty(t, token, "fresh provision must generate a non-empty token")
	assert.Len(t, token, 64, "token must be 64 hex chars (32 bytes entropy)")
}
