// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package email

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNoopProvider_Send_ReturnsNil(t *testing.T) {
	err := (&NoopProvider{}).Send(context.Background(), Message{
		To:       "ops@test.com",
		Subject:  "test",
		TextBody: "body",
	})
	assert.NoError(t, err, "NoopProvider.Send must never error")
}

func TestNoopProvider_Send_EmptyMessage_NoPanic(t *testing.T) {
	err := (&NoopProvider{}).Send(context.Background(), Message{})
	assert.NoError(t, err)
}

func TestNoopProvider_Send_CancelledContext_NoPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (&NoopProvider{}).Send(ctx, Message{To: "x@y.com"})
	assert.NoError(t, err, "NoopProvider ignores context (it just logs)")
}
