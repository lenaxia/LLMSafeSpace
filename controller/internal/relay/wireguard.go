// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

const defaultPersistentKeepalive = 25
const defaultListenPort = 51820

// Keypair holds a WireGuard X25519 keypair in base64-encoded form,
// matching the format used by wg-quick and wg set commands.
type Keypair struct {
	PrivateKeyB64 string
	PublicKeyB64  string
}

// GenerateKeypair produces a fresh WireGuard keypair using crypto/rand
// and X25519 scalar multiplication.
func GenerateKeypair() (Keypair, error) {
	curve := ecdh.X25519()
	privKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return Keypair{}, fmt.Errorf("generate X25519 key: %w", err)
	}
	return Keypair{
		PrivateKeyB64: base64.StdEncoding.EncodeToString(privKey.Bytes()),
		PublicKeyB64:  base64.StdEncoding.EncodeToString(privKey.PublicKey().Bytes()),
	}, nil
}

// DerivePublicKey computes the X25519 public key from a base64-encoded
// private key. Used for verification and key recovery.
func DerivePublicKey(privateKeyB64 string) (string, error) {
	privBytes, err := base64.StdEncoding.DecodeString(privateKeyB64)
	if err != nil {
		return "", fmt.Errorf("decode private key: %w", err)
	}
	if len(privBytes) != 32 {
		return "", fmt.Errorf("invalid private key length: got %d, want 32", len(privBytes))
	}
	curve := ecdh.X25519()
	privKey, err := curve.NewPrivateKey(privBytes)
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(privKey.PublicKey().Bytes()), nil
}

// RelayWGConfig holds the parameters for a relay VM's wg0.conf.
type RelayWGConfig struct {
	PrivateKey          string
	WgIP                string
	RouterPublicKey     string
	RouterEndpoint      string
	AllowedIPs          string
	PersistentKeepalive int
}

// RenderRelayConfig renders a WireGuard configuration file for a relay VM.
// The relay VM connects to the router as a single peer (star topology).
func RenderRelayConfig(cfg RelayWGConfig) (string, error) {
	if cfg.PrivateKey == "" {
		return "", fmt.Errorf("private key is required")
	}
	if cfg.WgIP == "" {
		return "", fmt.Errorf("WireGuard IP is required")
	}
	if cfg.RouterPublicKey == "" {
		return "", fmt.Errorf("router public key is required")
	}
	if cfg.RouterEndpoint == "" {
		return "", fmt.Errorf("router endpoint is required")
	}
	if cfg.AllowedIPs == "" {
		cfg.AllowedIPs = "10.42.42.0/24"
	}
	if cfg.PersistentKeepalive == 0 {
		cfg.PersistentKeepalive = defaultPersistentKeepalive
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", cfg.PrivateKey)
	fmt.Fprintf(&b, "Address = %s/24\n", cfg.WgIP)
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", cfg.RouterPublicKey)
	fmt.Fprintf(&b, "Endpoint = %s\n", cfg.RouterEndpoint)
	fmt.Fprintf(&b, "AllowedIPs = %s\n", cfg.AllowedIPs)
	fmt.Fprintf(&b, "PersistentKeepalive = %d\n", cfg.PersistentKeepalive)
	return b.String(), nil
}

// RouterPeer represents a single relay VM peer in the router's WireGuard config.
type RouterPeer struct {
	PublicKey  string
	AllowedIPs string
}

// RouterWGConfig holds the parameters for the relay-router's wg0.conf.
type RouterWGConfig struct {
	PrivateKey string
	WgIP       string
	ListenPort int
	Peers      []RouterPeer
}

// RenderRouterConfig renders the WireGuard configuration for the relay-router.
// The router listens for incoming connections from relay VMs (server role).
func RenderRouterConfig(cfg RouterWGConfig) (string, error) {
	if cfg.PrivateKey == "" {
		return "", fmt.Errorf("router private key is required")
	}
	if cfg.WgIP == "" {
		return "", fmt.Errorf("router WireGuard IP is required")
	}
	if len(cfg.Peers) == 0 {
		return "", fmt.Errorf("at least one peer is required")
	}
	for i, p := range cfg.Peers {
		if p.PublicKey == "" {
			return "", fmt.Errorf("peer %d: public key is required", i)
		}
		if p.AllowedIPs == "" {
			return "", fmt.Errorf("peer %d: allowed IPs is required", i)
		}
	}
	if cfg.ListenPort == 0 {
		cfg.ListenPort = defaultListenPort
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", cfg.PrivateKey)
	fmt.Fprintf(&b, "Address = %s/24\n", cfg.WgIP)
	fmt.Fprintf(&b, "ListenPort = %d\n", cfg.ListenPort)

	for _, peer := range cfg.Peers {
		fmt.Fprintf(&b, "\n")
		fmt.Fprintf(&b, "[Peer]\n")
		fmt.Fprintf(&b, "PublicKey = %s\n", peer.PublicKey)
		fmt.Fprintf(&b, "AllowedIPs = %s\n", peer.AllowedIPs)
	}
	return b.String(), nil
}
