// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestGetEnvDuration_InvalidValue verifies the fallback when the env var holds
// an unparseable duration string (the err != nil branch in getEnvDuration).
func TestGetEnvDuration_InvalidValue(t *testing.T) {
	t.Setenv("BAD_DURATION", "not-a-duration")
	got := getEnvDuration("BAD_DURATION", 42*time.Second)
	assert.Equal(t, 42*time.Second, got,
		"invalid duration env must fall back to the default")
}

// TestGetEnvDuration_ValidValue verifies the happy path.
func TestGetEnvDuration_ValidValue(t *testing.T) {
	t.Setenv("GOOD_DURATION", "7m")
	got := getEnvDuration("GOOD_DURATION", 42*time.Second)
	assert.Equal(t, 7*time.Minute, got)
}

// TestGetEnvDuration_EmptyEnv verifies empty env falls back to default.
func TestGetEnvDuration_EmptyEnv(t *testing.T) {
	t.Setenv("EMPTY_DURATION", "")
	got := getEnvDuration("EMPTY_DURATION", 42*time.Second)
	assert.Equal(t, 42*time.Second, got)
}

// TestGetEnv_Fallback verifies getEnv returns the fallback when env is empty.
func TestGetEnv_Fallback(t *testing.T) {
	assert.Equal(t, "default", getEnv("NONEXISTENT_KEY_12345", "default"))
}

// TestGetEnv_SetValue verifies getEnv returns the env value when set.
func TestGetEnv_SetValue(t *testing.T) {
	t.Setenv("MY_KEY", "from-env")
	assert.Equal(t, "from-env", getEnv("MY_KEY", "default"))
}

// TestDefaultHTTPClient verifies the constructor returns a non-nil client with
// a configured transport. Previously 0% coverage (only called in main()).
func TestDefaultHTTPClient(t *testing.T) {
	c := defaultHTTPClient()
	if assert.NotNil(t, c) {
		assert.NotNil(t, c.Transport, "client must have a transport configured")
	}
}
