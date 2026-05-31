// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package utilities

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashString creates a SHA-256 hash of the input string and returns it as a hex string
func HashString(input string) string {
	hash := sha256.Sum256([]byte(input))
	return hex.EncodeToString(hash[:])
}
