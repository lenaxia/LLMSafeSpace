// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	emailsvc "github.com/lenaxia/llmsafespaces/api/internal/services/email"
	"github.com/lenaxia/llmsafespaces/pkg/email"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEmailProvider captures the last message and returns the configured
// error so tests can exercise send-failure paths.
type fakeEmailProvider struct {
	last *email.Message
	err  error
}

func (f *fakeEmailProvider) Send(_ context.Context, msg email.Message) error {
	f.last = &msg
	return f.err
}

type testSendErr struct{ msg string }

func (e *testSendErr) Error() string { return e.msg }

func setupEmailRouter(h *EmailHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/admin/email/test", h.TestSend)
	return r
}

func TestEmailHandler_TestSend_SESSuccess(t *testing.T) {
	fp := &fakeEmailProvider{}
	svc := emailsvc.NewService(fp, "https://app.test", "ses")
	h := NewEmailHandler(svc)

	router := setupEmailRouter(h)
	w := doRequest(router, http.MethodPost, "/admin/email/test", `{"to":"ops@test.com"}`)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["sent"])
	assert.Equal(t, "ses", resp["provider"])
}

func TestEmailHandler_TestSend_NoopReportsNotSent(t *testing.T) {
	svc := emailsvc.NewService(&email.NoopProvider{}, "https://app.test", "")
	h := NewEmailHandler(svc)

	router := setupEmailRouter(h)
	w := doRequest(router, http.MethodPost, "/admin/email/test", `{"to":"ops@test.com"}`)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["sent"])
	assert.Equal(t, "noop", resp["provider"])
}

func TestEmailHandler_TestSend_MissingTo_Rejected(t *testing.T) {
	svc := emailsvc.NewService(&fakeEmailProvider{}, "https://app.test", "ses")
	h := NewEmailHandler(svc)

	router := setupEmailRouter(h)
	w := doRequest(router, http.MethodPost, "/admin/email/test", `{}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestEmailHandler_TestSend_SendError_Returns502(t *testing.T) {
	fp := &fakeEmailProvider{err: &testSendErr{msg: "Email address is not verified."}}
	svc := emailsvc.NewService(fp, "https://app.test", "ses")
	h := NewEmailHandler(svc)

	router := setupEmailRouter(h)
	w := doRequest(router, http.MethodPost, "/admin/email/test", `{"to":"unverified@test.com"}`)
	// A send failure is reported to the admin as 502 (upstream email provider
	// rejected) so the admin gets actionable feedback.
	require.Equal(t, http.StatusBadGateway, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "email send failed")
}

func TestEmailHandler_TestSend_InvalidEmail_Rejected(t *testing.T) {
	svc := emailsvc.NewService(&fakeEmailProvider{}, "https://app.test", "ses")
	h := NewEmailHandler(svc)

	router := setupEmailRouter(h)
	w := doRequest(router, http.MethodPost, "/admin/email/test", `{"to":"not-an-email"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestEmailHandler_TestSend_NilProvider_ReportsNoop(t *testing.T) {
	// Defensive path: a Service constructed with a nil provider (email not
	// configured). The handler must report noop rather than panic.
	svc := emailsvc.NewService(nil, "https://app.test", "ses")
	h := NewEmailHandler(svc)

	router := setupEmailRouter(h)
	w := doRequest(router, http.MethodPost, "/admin/email/test", `{"to":"ops@test.com"}`)
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["sent"])
	assert.Equal(t, "noop", resp["provider"])
}
