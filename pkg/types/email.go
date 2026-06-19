// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package types

import "time"

// EmailToken is a single-use token for password reset or email verification.
// Only the sha256 hash is stored; the raw token is presented to the user via
// email and consumed on first POST (never via GET — scanner defence, US-49.9).
type EmailToken struct {
	ID         string     `json:"id" db:"id"`
	UserID     string     `json:"userId" db:"user_id"`
	Kind       string     `json:"kind" db:"kind"` // "password_reset" | "email_verify"
	TokenHash  string     `json:"-" db:"token_hash"`
	ExpiresAt  time.Time  `json:"expiresAt" db:"expires_at"`
	ConsumedAt *time.Time `json:"consumedAt,omitempty" db:"consumed_at"`
}
