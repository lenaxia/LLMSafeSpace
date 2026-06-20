// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier:AGPL-3.0-or-later

package email

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMockSESProvider creates an SESProvider pointed at an httptest server.
// The server returns the configured status + response. This lets us test
// Send without real AWS credentials.
func newMockSESProvider(t *testing.T, status int, respBody string) (*SESProvider, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(func() { server.Close() })

	cfg := aws.Config{
		Region: "us-east-1",
	}
	client := ses.NewFromConfig(cfg, func(o *ses.Options) {
		o.BaseEndpoint = aws.String(server.URL)
	})
	return &SESProvider{client: client, from: "noreply@test.com"}, server
}

func TestNewSESProvider_ConstructsWithoutPanic(t *testing.T) {
	p := NewSESProvider(aws.Config{Region: "us-east-1"}, "noreply@test.com")
	require.NotNil(t, p)
	assert.Equal(t, "noreply@test.com", p.from)
}

func TestSESProvider_Send_Success(t *testing.T) {
	// SES returns XML. A minimal successful response:
	provider, _ := newMockSESProvider(t, http.StatusOK,
		`<SendEmailResponse xmlns="http://ses.amazonaws.com/doc/2010-12-01/"><SendEmailResult><MessageId>msg-123</MessageId></SendEmailResult></SendEmailResponse>`)

	err := provider.Send(context.Background(), Message{
		To:       "alice@test.com",
		Subject:  "Hello",
		TextBody: "plain text",
		HTMLBody: "<p>html</p>",
	})
	require.NoError(t, err, "successful SES response must not error")
}

func TestSESProvider_Send_SESError_Wrapped(t *testing.T) {
	provider, _ := newMockSESProvider(t, http.StatusBadRequest,
		`<ErrorResponse><Error><Code>MessageRejected</Code><Message>Email address is not verified.</Message></Error></ErrorResponse>`)

	err := provider.Send(context.Background(), Message{
		To:      "unverified@test.com",
		Subject: "test",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ses send email to unverified@test.com")
}

func TestSESProvider_Send_CancelledContext(t *testing.T) {
	provider, _ := newMockSESProvider(t, http.StatusOK,
		`<SendEmailResponse xmlns="http://ses.amazonaws.com/doc/2010-12-01/"><SendEmailResult><MessageId>ok</MessageId></SendEmailResult></SendEmailResponse>`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := provider.Send(ctx, Message{
		To:      "alice@test.com",
		Subject: "test",
	})
	require.Error(t, err, "cancelled context must produce an error")
}

func TestSESProvider_Send_5xxError_Wrapped(t *testing.T) {
	provider, _ := newMockSESProvider(t, http.StatusInternalServerError,
		`<ErrorResponse><Error><Code>InternalError</Code><Message>internal</Message></Error></ErrorResponse>`)

	err := provider.Send(context.Background(), Message{
		To:      "alice@test.com",
		Subject: "test",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ses send email")
}
