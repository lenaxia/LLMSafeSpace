// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

import (
	"net/http"
	"testing"

	"github.com/lenaxia/llmsafespace/pkg/agentd"
)

const testPassword = "test-pw"

func requireAuth(t *testing.T, next http.Handler) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pw, ok := r.BasicAuth()
		if !ok || user != agentd.AuthUsername || pw != testPassword {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
