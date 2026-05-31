// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package http

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// setupContext creates a gin.Context backed by an httptest.ResponseRecorder
// and returns both the context and the recorder so callers can inspect
// the HTTP response independently.
func setupContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/", nil)
	return c, w
}

// ---------------------------------------------------------------------------
// NewBodyCaptureWriter
// ---------------------------------------------------------------------------

func TestNewBodyCaptureWriter_NotNil(t *testing.T) {
	c, _ := setupContext()
	bw := NewBodyCaptureWriter(c)
	require.NotNil(t, bw)
	require.NotNil(t, bw.Body)
}

func TestNewBodyCaptureWriter_StartsEmpty(t *testing.T) {
	c, _ := setupContext()
	bw := NewBodyCaptureWriter(c)
	assert.Equal(t, "", bw.GetBody())
}

// ---------------------------------------------------------------------------
// Write
// ---------------------------------------------------------------------------

func TestWrite_CapturesAndForwards(t *testing.T) {
	c, rec := setupContext()
	bw := NewBodyCaptureWriter(c)
	// Replace the context writer so the underlying recorder receives output
	c.Writer = bw

	payload := []byte("hello world")
	n, err := bw.Write(payload)

	require.NoError(t, err)
	assert.Equal(t, len(payload), n)

	// Captured in the buffer
	assert.Equal(t, "hello world", bw.GetBody())

	// Also forwarded to the underlying response recorder
	assert.Equal(t, "hello world", rec.Body.String())
}

func TestWrite_MultipleWrites_Accumulate(t *testing.T) {
	c, _ := setupContext()
	bw := NewBodyCaptureWriter(c)
	c.Writer = bw

	_, _ = bw.Write([]byte("foo"))
	_, _ = bw.Write([]byte("bar"))

	assert.Equal(t, "foobar", bw.GetBody())
}

func TestWrite_EmptySlice(t *testing.T) {
	c, _ := setupContext()
	bw := NewBodyCaptureWriter(c)
	c.Writer = bw

	n, err := bw.Write([]byte{})
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Equal(t, "", bw.GetBody())
}

// ---------------------------------------------------------------------------
// WriteString
// ---------------------------------------------------------------------------

func TestWriteString_CapturesAndForwards(t *testing.T) {
	c, rec := setupContext()
	bw := NewBodyCaptureWriter(c)
	c.Writer = bw

	n, err := bw.WriteString("hello string")

	require.NoError(t, err)
	assert.Equal(t, len("hello string"), n)
	assert.Equal(t, "hello string", bw.GetBody())
	assert.Equal(t, "hello string", rec.Body.String())
}

func TestWriteString_MultipleWrites_Accumulate(t *testing.T) {
	c, _ := setupContext()
	bw := NewBodyCaptureWriter(c)
	c.Writer = bw

	_, _ = bw.WriteString("ping")
	_, _ = bw.WriteString("pong")

	assert.Equal(t, "pingpong", bw.GetBody())
}

// ---------------------------------------------------------------------------
// GetBody
// ---------------------------------------------------------------------------

func TestGetBody_ReturnsBufferContents(t *testing.T) {
	c, _ := setupContext()
	bw := NewBodyCaptureWriter(c)
	bw.Body = bytes.NewBufferString("already written")

	assert.Equal(t, "already written", bw.GetBody())
}

func TestGetBody_EmptyAfterReset(t *testing.T) {
	c, _ := setupContext()
	bw := NewBodyCaptureWriter(c)
	c.Writer = bw

	_, _ = bw.WriteString("some data")
	bw.Body.Reset()
	assert.Equal(t, "", bw.GetBody())
}

// ---------------------------------------------------------------------------
// Mixed Write / WriteString
// ---------------------------------------------------------------------------

func TestMixedWriteMethods(t *testing.T) {
	c, _ := setupContext()
	bw := NewBodyCaptureWriter(c)
	c.Writer = bw

	_, _ = bw.Write([]byte("bytes"))
	_, _ = bw.WriteString("-string")

	assert.Equal(t, "bytes-string", bw.GetBody())
}
