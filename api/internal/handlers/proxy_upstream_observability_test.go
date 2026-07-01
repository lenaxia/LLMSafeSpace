// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// LLMSafeSpaces#488: pure-function unit tests for the observability
// helpers in proxy_upstream_observability.go. These complement the
// integration tests in proxy_upstream_5xx_observability_test.go which
// exercise the whole path through httptest. Integration tests are
// coarse; these unit tests cover the branches integration tests
// don't naturally hit — chiefly the body-preview truncation cap and
// the path-sanitization edge cases.

func TestSanitizePathForMetric_TableDriven(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			// The exact path #486 hit.
			"history endpoint",
			"/session/ses_0e84b0f4fffehGzfK2aH76IGOY/message",
			"/session/:id/message",
		},
		{
			// POST /session/:id — abort, session detail, etc.
			"session detail endpoint",
			"/session/ses_abc123",
			"/session/:id",
		},
		{
			// POST /session/:id/prompt_async.
			"prompt async endpoint",
			"/session/ses_xyz789/prompt_async",
			"/session/:id/prompt_async",
		},
		{
			// No session ID present — unchanged.
			"root session list",
			"/session",
			"/session",
		},
		{
			// Non-session paths pass through verbatim.
			"providers listing",
			"/provider",
			"/provider",
		},
		{
			// Multiple session IDs in the same path (unusual but real
			// for /session/:id/subsession/:sid shapes) — all collapse.
			"nested session id path",
			"/session/ses_parent/relate/ses_child",
			"/session/:id/relate/:id",
		},
		{
			// Empty path — must not panic; passes through.
			"empty path",
			"",
			"",
		},
		{
			// Leading segment that starts with `ses_` but is not a
			// session ID would still be collapsed. This is a known
			// bound of the current sanitizer — session IDs are
			// prefixed `ses_` by convention and no other opencode
			// path segment starts with that prefix, so it's safe in
			// practice. Documented here so a future regression that
			// broadens what `ses_` matches is caught.
			"any ses_ prefixed segment",
			"/ses_prefixed_leaf/foo",
			"/:id/foo",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizePathForMetric(tc.input)
			assert.Equal(t, tc.want, got,
				"sanitizePathForMetric(%q) = %q; want %q", tc.input, got, tc.want)
		})
	}
}

func TestRecordUpstream5xx_BodyPreviewCap(t *testing.T) {
	// The body preview cap is documented at 512 bytes. This test
	// exercises the truncation branch that the integration tests
	// (which use ~100-byte error envelopes) never trigger.
	logger := &proxyCaptureLogger{}

	// 600-byte body > 512-byte cap.
	body := []byte(strings.Repeat("A", 600))

	recordUpstream5xx(logger, "ws-preview", "/session/:id/message", 500, body)

	line := logger.findWarn("Upstream 5xx")
	if line == nil {
		t.Fatal("expected Warn log line")
	}

	preview, ok := line.fields["bodyPreview"].(string)
	if !ok {
		t.Fatalf("bodyPreview must be a string; got %T", line.fields["bodyPreview"])
	}

	// Preview is capped at 512 bytes + the "..." suffix (3 bytes).
	// Total length is len(cap) + 3.
	assert.Equal(t, 512+3, len(preview),
		"preview must be capped at %d chars + '...' suffix (got %d)", 512, len(preview))
	assert.True(t, strings.HasSuffix(preview, "..."),
		"truncated preview must end with '...' so operators can tell it was cut")
	// The first 512 bytes are the original body.
	assert.Equal(t, strings.Repeat("A", 512), preview[:512],
		"preview must be a strict prefix of the original body (no re-encoding)")
}

func TestRecordUpstream5xx_BodyPreviewUncappedWhenSmall(t *testing.T) {
	// Small bodies (<= 512 bytes) should render verbatim, no "..." suffix.
	logger := &proxyCaptureLogger{}

	body := []byte(`{"name":"UnknownError","data":{"ref":"err_abcdef12"}}`)
	recordUpstream5xx(logger, "ws-small", "/session/:id/message", 500, body)

	line := logger.findWarn("Upstream 5xx")
	if line == nil {
		t.Fatal("expected Warn log line")
	}
	preview, _ := line.fields["bodyPreview"].(string)
	assert.Equal(t, string(body), preview,
		"preview must equal the full body verbatim when under the cap")
	assert.False(t, strings.HasSuffix(preview, "..."),
		"under-cap preview must NOT have the truncation suffix")
}

func TestRecordUpstream5xx_NilBodyProducesEmptyPreview(t *testing.T) {
	// The streaming proxy path (doProxy) calls recordUpstream5xx with
	// body=nil because the response body is streaming downstream to the
	// client and cannot be peeked. Logger + counter still fire but the
	// preview is empty. This is documented in the code comment; test
	// pins the behavior.
	logger := &proxyCaptureLogger{}

	recordUpstream5xx(logger, "ws-nobody", "/session", 502, nil)

	line := logger.findWarn("Upstream 5xx")
	if line == nil {
		t.Fatal("expected Warn log line")
	}
	preview, _ := line.fields["bodyPreview"].(string)
	assert.Equal(t, "", preview,
		"nil body must produce empty preview — streaming-path documented behavior")
	// Other fields still populated.
	assert.Equal(t, "ws-nobody", line.fields["workspaceID"])
	assert.Equal(t, "/session", line.fields["path"])
	assert.Equal(t, 502, line.fields["upstreamStatus"])
}

func TestRecordUpstream5xx_NilLoggerDoesNotPanic(t *testing.T) {
	// Defensive: the logger field on ProxyHandler is set at construction
	// but could theoretically be nil in a partially-initialized handler
	// (e.g. during a shutdown race). Metric still fires, no log line,
	// no panic.
	assert.NotPanics(t, func() {
		recordUpstream5xx(nil, "ws-nil-logger", "/session", 500, nil)
	})
}
