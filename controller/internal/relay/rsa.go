// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
)

// parseRSAPrivateKeyPEM parses a PEM-encoded RSA private key (PKCS#1 or PKCS#8).
func parseRSAPrivateKeyPEM(pemStr string) (*rsa.PrivateKey, error) {
	// Normalize PEM block
	pemStr = strings.TrimSpace(pemStr)
	if !strings.HasPrefix(pemStr, "-----BEGIN") {
		pemStr = "-----BEGIN RSA PRIVATE KEY-----\n" + pemStr + "\n-----END RSA PRIVATE KEY-----"
	}

	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	// Try PKCS#1 first (RSA PRIVATE KEY)
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	// Try PKCS#8 (PRIVATE KEY)
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key (tried PKCS#1 and PKCS#8): %w", err)
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not RSA (got %T)", key)
	}

	return rsaKey, nil
}

// rsaSignSHA256 signs data with RSA-SHA256 using PKCS#1 v1.5.
func rsaSignSHA256(privKey *rsa.PrivateKey, data []byte) ([]byte, error) {
	hash := sha256.Sum256(data)
	return rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, hash[:])
}
