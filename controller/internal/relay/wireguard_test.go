// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"crypto/ecdh"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Keypair generation tests
// ---------------------------------------------------------------------------

func TestGenerateKeypair_ValidFormat(t *testing.T) {
	kp, err := GenerateKeypair()
	require.NoError(t, err)
	require.NotEmpty(t, kp.PrivateKeyB64)
	require.NotEmpty(t, kp.PublicKeyB64)

	privBytes, err := base64.StdEncoding.DecodeString(kp.PrivateKeyB64)
	require.NoError(t, err)
	assert.Len(t, privBytes, 32, "private key must be 32 bytes")

	pubBytes, err := base64.StdEncoding.DecodeString(kp.PublicKeyB64)
	require.NoError(t, err)
	assert.Len(t, pubBytes, 32, "public key must be 32 bytes")
}

func TestGenerateKeypair_PublicDerivesFromPrivate(t *testing.T) {
	kp, err := GenerateKeypair()
	require.NoError(t, err)

	privBytes, err := base64.StdEncoding.DecodeString(kp.PrivateKeyB64)
	require.NoError(t, err)

	curve := ecdh.X25519()
	privKey, err := curve.NewPrivateKey(privBytes)
	require.NoError(t, err)
	expectedPub := base64.StdEncoding.EncodeToString(privKey.PublicKey().Bytes())

	assert.Equal(t, expectedPub, kp.PublicKeyB64, "public key must derive from private key via X25519")
}

func TestGenerateKeypair_UniqueEachCall(t *testing.T) {
	keys := make(map[string]bool, 20)
	for i := 0; i < 20; i++ {
		kp, err := GenerateKeypair()
		require.NoError(t, err)
		assert.False(t, keys[kp.PrivateKeyB64], "duplicate private key generated on iteration %d", i)
		keys[kp.PrivateKeyB64] = true
	}
}

func TestGenerateKeypair_PrivateKeyIsClamped(t *testing.T) {
	for i := 0; i < 50; i++ {
		kp, err := GenerateKeypair()
		require.NoError(t, err)

		privBytes, err := base64.StdEncoding.DecodeString(kp.PrivateKeyB64)
		require.NoError(t, err)

		curve := ecdh.X25519()
		_, err = curve.NewPrivateKey(privBytes)
		require.NoError(t, err, "private key must be a valid X25519 scalar (clamped by ecdh)")
	}
}

// ---------------------------------------------------------------------------
// DerivePublicKey tests
// ---------------------------------------------------------------------------

func TestDerivePublicKey_FromValidPrivate(t *testing.T) {
	kp, err := GenerateKeypair()
	require.NoError(t, err)

	derived, err := DerivePublicKey(kp.PrivateKeyB64)
	require.NoError(t, err)
	assert.Equal(t, kp.PublicKeyB64, derived)
}

func TestDerivePublicKey_InvalidBase64(t *testing.T) {
	_, err := DerivePublicKey("not-valid-base64!!!")
	assert.Error(t, err)
}

func TestDerivePublicKey_WrongLength(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("too-short"))
	_, err := DerivePublicKey(short)
	assert.Error(t, err)
}

func TestDerivePublicKey_EmptyInput(t *testing.T) {
	_, err := DerivePublicKey("")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// RelayConfig rendering tests
// ---------------------------------------------------------------------------

func TestRenderRelayConfig_AllFields(t *testing.T) {
	cfg := RelayWGConfig{
		PrivateKey:          "relay-private-key-base64",
		WgIP:                "10.42.42.2",
		RouterPublicKey:     "router-public-key-base64",
		RouterEndpoint:      "relay-gw.safespaces.dev:51820",
		AllowedIPs:          "10.42.42.0/24",
		PersistentKeepalive: 25,
	}

	out, err := RenderRelayConfig(cfg)
	require.NoError(t, err)

	assert.Contains(t, out, "[Interface]")
	assert.Contains(t, out, "PrivateKey = relay-private-key-base64")
	assert.Contains(t, out, "Address = 10.42.42.2/24")
	assert.Contains(t, out, "[Peer]")
	assert.Contains(t, out, "PublicKey = router-public-key-base64")
	assert.Contains(t, out, "Endpoint = relay-gw.safespaces.dev:51820")
	assert.Contains(t, out, "AllowedIPs = 10.42.42.0/24")
	assert.Contains(t, out, "PersistentKeepalive = 25")
}

func TestRenderRelayConfig_Ordering(t *testing.T) {
	cfg := RelayWGConfig{
		PrivateKey:          "priv",
		WgIP:                "10.42.42.2",
		RouterPublicKey:     "pub",
		RouterEndpoint:      "ep:51820",
		AllowedIPs:          "10.42.42.0/24",
		PersistentKeepalive: 25,
	}

	out, err := RenderRelayConfig(cfg)
	require.NoError(t, err)

	ifaceIdx := strings.Index(out, "[Interface]")
	peerIdx := strings.Index(out, "[Peer]")
	assert.Greater(t, ifaceIdx, -1)
	assert.Greater(t, peerIdx, -1)
	assert.Less(t, ifaceIdx, peerIdx, "[Interface] must come before [Peer]")
}

func TestRenderRelayConfig_DefaultKeepalive(t *testing.T) {
	cfg := RelayWGConfig{
		PrivateKey:      "priv",
		WgIP:            "10.42.42.2",
		RouterPublicKey: "pub",
		RouterEndpoint:  "ep:51820",
		AllowedIPs:      "10.42.42.0/24",
	}

	out, err := RenderRelayConfig(cfg)
	require.NoError(t, err)
	assert.Contains(t, out, "PersistentKeepalive = 25", "default keepalive must be 25")
}

func TestRenderRelayConfig_EmptyPrivateKey(t *testing.T) {
	cfg := RelayWGConfig{
		WgIP:            "10.42.42.2",
		RouterPublicKey: "pub",
		RouterEndpoint:  "ep:51820",
		AllowedIPs:      "10.42.42.0/24",
	}

	_, err := RenderRelayConfig(cfg)
	assert.Error(t, err)
}

func TestRenderRelayConfig_EmptyWgIP(t *testing.T) {
	cfg := RelayWGConfig{
		PrivateKey:      "priv",
		RouterPublicKey: "pub",
		RouterEndpoint:  "ep:51820",
		AllowedIPs:      "10.42.42.0/24",
	}

	_, err := RenderRelayConfig(cfg)
	assert.Error(t, err)
}

func TestRenderRelayConfig_EmptyRouterPublicKey(t *testing.T) {
	cfg := RelayWGConfig{
		PrivateKey:     "priv",
		WgIP:           "10.42.42.2",
		RouterEndpoint: "ep:51820",
		AllowedIPs:     "10.42.42.0/24",
	}

	_, err := RenderRelayConfig(cfg)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// RouterConfig rendering tests
// ---------------------------------------------------------------------------

func TestRenderRouterConfig_SinglePeer(t *testing.T) {
	cfg := RouterWGConfig{
		PrivateKey: "router-priv-key",
		WgIP:       "10.42.42.1",
		ListenPort: 51820,
		Peers: []RouterPeer{
			{
				PublicKey:  "relay-1-pub",
				AllowedIPs: "10.42.42.2/32",
			},
		},
	}

	out, err := RenderRouterConfig(cfg)
	require.NoError(t, err)

	assert.Contains(t, out, "[Interface]")
	assert.Contains(t, out, "PrivateKey = router-priv-key")
	assert.Contains(t, out, "Address = 10.42.42.1/24")
	assert.Contains(t, out, "ListenPort = 51820")
	assert.Contains(t, out, "[Peer]")
	assert.Contains(t, out, "PublicKey = relay-1-pub")
	assert.Contains(t, out, "AllowedIPs = 10.42.42.2/32")
	assert.NotContains(t, out, "Endpoint", "router does not set Endpoint on peers (relay VMs connect to router)")
}

func TestRenderRouterConfig_MultiplePeers(t *testing.T) {
	cfg := RouterWGConfig{
		PrivateKey: "router-priv-key",
		WgIP:       "10.42.42.1",
		ListenPort: 51820,
		Peers: []RouterPeer{
			{PublicKey: "oci-pub", AllowedIPs: "10.42.42.2/32"},
			{PublicKey: "gcp-pub", AllowedIPs: "10.42.42.3/32"},
		},
	}

	out, err := RenderRouterConfig(cfg)
	require.NoError(t, err)

	assert.Equal(t, 2, strings.Count(out, "[Peer]"), "must have exactly 2 [Peer] blocks")
	assert.Contains(t, out, "oci-pub")
	assert.Contains(t, out, "gcp-pub")
}

func TestRenderRouterConfig_EmptyPrivateKey(t *testing.T) {
	cfg := RouterWGConfig{
		WgIP:       "10.42.42.1",
		ListenPort: 51820,
		Peers:      []RouterPeer{{PublicKey: "pub", AllowedIPs: "10.42.42.2/32"}},
	}

	_, err := RenderRouterConfig(cfg)
	assert.Error(t, err)
}

func TestRenderRouterConfig_NoPeers(t *testing.T) {
	cfg := RouterWGConfig{
		PrivateKey: "router-priv-key",
		WgIP:       "10.42.42.1",
		ListenPort: 51820,
	}

	_, err := RenderRouterConfig(cfg)
	assert.Error(t, err)
}

func TestRenderRouterConfig_PeerMissingPublicKey(t *testing.T) {
	cfg := RouterWGConfig{
		PrivateKey: "router-priv-key",
		WgIP:       "10.42.42.1",
		ListenPort: 51820,
		Peers: []RouterPeer{
			{AllowedIPs: "10.42.42.2/32"},
		},
	}

	_, err := RenderRouterConfig(cfg)
	assert.Error(t, err)
}

func TestRenderRouterConfig_DefaultListenPort(t *testing.T) {
	cfg := RouterWGConfig{
		PrivateKey: "router-priv-key",
		WgIP:       "10.42.42.1",
		Peers:      []RouterPeer{{PublicKey: "pub", AllowedIPs: "10.42.42.2/32"}},
	}

	out, err := RenderRouterConfig(cfg)
	require.NoError(t, err)
	assert.Contains(t, out, "ListenPort = 51820")
}

// ---------------------------------------------------------------------------
// Integration: generate keypair → render relay config → verify round-trip
// ---------------------------------------------------------------------------

func TestIntegration_KeypairRendersIntoConfig(t *testing.T) {
	relayKP, err := GenerateKeypair()
	require.NoError(t, err)

	routerKP, err := GenerateKeypair()
	require.NoError(t, err)

	relayCfg, err := RenderRelayConfig(RelayWGConfig{
		PrivateKey:          relayKP.PrivateKeyB64,
		WgIP:                "10.42.42.2",
		RouterPublicKey:     routerKP.PublicKeyB64,
		RouterEndpoint:      "relay-gw.safespaces.dev:51820",
		AllowedIPs:          "10.42.42.0/24",
		PersistentKeepalive: 25,
	})
	require.NoError(t, err)

	routerCfg, err := RenderRouterConfig(RouterWGConfig{
		PrivateKey: routerKP.PrivateKeyB64,
		WgIP:       "10.42.42.1",
		ListenPort: 51820,
		Peers: []RouterPeer{
			{PublicKey: relayKP.PublicKeyB64, AllowedIPs: "10.42.42.2/32"},
		},
	})
	require.NoError(t, err)

	assert.Contains(t, relayCfg, relayKP.PrivateKeyB64)
	assert.Contains(t, relayCfg, routerKP.PublicKeyB64)
	assert.Contains(t, routerCfg, routerKP.PrivateKeyB64)
	assert.Contains(t, routerCfg, relayKP.PublicKeyB64)

	derived, err := DerivePublicKey(relayKP.PrivateKeyB64)
	require.NoError(t, err)
	assert.Equal(t, relayKP.PublicKeyB64, derived)
}
