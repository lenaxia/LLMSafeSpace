// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package email

import (
	"context"
	"testing"

	"github.com/lenaxia/llmsafespaces/pkg/email"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProvider captures the last message handed to Send. Returns the
// configured error so tests can exercise send-failure paths.
type fakeProvider struct {
	last    *email.Message
	sendErr error
}

func (f *fakeProvider) Send(_ context.Context, msg email.Message) error {
	f.last = &msg
	return f.sendErr
}

func newSvc() (*Service, *fakeProvider) {
	fp := &fakeProvider{}
	return NewService(fp, "https://app.test", "ses"), fp
}

func TestNewService_NilProviderIsSafeToSendTest(t *testing.T) {
	// A nil provider means "email not configured". SendTest must not panic;
	// it returns ErrNotConfigured so the handler can report provider=noop.
	svc := NewService(nil, "https://app.test", "")
	err := svc.SendTest(context.Background(), "ops@test.com")
	assert.ErrorIs(t, err, ErrNotConfigured)
}

func TestProviderName(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"empty defaults to noop", "", "noop"},
		{"explicit noop", "noop", "noop"},
		{"ses", "ses", "ses"},
		{"unknown normalised to noop", "smtp", "noop"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeProvider{}, "https://app.test", tt.input)
			assert.Equal(t, tt.expect, svc.ProviderName())
		})
	}
}

func TestSendTest_BuildsMessageAndSends(t *testing.T) {
	svc, fp := newSvc()
	err := svc.SendTest(context.Background(), "ops@test.com")
	require.NoError(t, err)
	require.NotNil(t, fp.last)
	assert.Equal(t, "ops@test.com", fp.last.To)
	assert.Contains(t, fp.last.Subject, "LLMSafeSpaces")
	assert.NotEmpty(t, fp.last.TextBody)
	assert.NotEmpty(t, fp.last.HTMLBody)
}

func TestSend_PropagatesProviderError(t *testing.T) {
	fp := &fakeProvider{sendErr: &testErr{msg: "ses down"}}
	svc := NewService(fp, "https://app.test", "ses")
	err := svc.SendTest(context.Background(), "ops@test.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ses down")
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }
