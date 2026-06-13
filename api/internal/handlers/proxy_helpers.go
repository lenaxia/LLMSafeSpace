// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

var blockedResponseHeaders = map[string]bool{
	"Www-Authenticate":   true,
	"Proxy-Authenticate": true,
	"Set-Cookie":         true,
}

func copyResponseHeaders(src http.Header, dst http.Header) {
	for k, vs := range src {
		if blockedResponseHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func stripVerboseQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return rawQuery
	}
	values.Del("verbose")
	values.Del("workspace")
	values.Del("directory")
	return values.Encode()
}

func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "network is unreachable")
}

func stripPatchParts(body []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return body, nil
	}

	switch trimmed[0] {
	case '{':
		var msg messageEnvelope
		if err := json.Unmarshal(body, &msg); err != nil {
			return nil, err
		}
		if msg.Parts == nil {
			return body, nil
		}
		msg.Parts = filterOutPatch(msg.Parts)
		return json.Marshal(msg)
	case '[':
		var msgs []messageEnvelope
		if err := json.Unmarshal(body, &msgs); err != nil {
			return nil, err
		}
		filteredAny := false
		for i, m := range msgs {
			if m.Parts != nil {
				msgs[i].Parts = filterOutPatch(m.Parts)
				filteredAny = true
			}
		}
		if !filteredAny {
			return body, nil
		}
		return json.Marshal(msgs)
	default:
		return body, nil
	}
}

func filterOutPatch(parts []json.RawMessage) []json.RawMessage {
	if len(parts) == 0 {
		return parts
	}
	out := make([]json.RawMessage, 0, len(parts))
	for _, p := range parts {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(p, &probe); err != nil {
			out = append(out, p)
			continue
		}
		if probe.Type == "patch" {
			continue
		}
		out = append(out, p)
	}
	return out
}

type messageEnvelope struct {
	Info  json.RawMessage   `json:"info,omitempty"`
	Parts []json.RawMessage `json:"parts"`
}
