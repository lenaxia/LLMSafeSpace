// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package canary provides the shared result type and assertion helpers used by
// all Go SDK canary functions.
package canary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Result is the JSON response written by every canary function.
type Result struct {
	Scenario  string  `json:"scenario"`
	SDK       string  `json:"sdk"`
	Passed    int     `json:"passed"`
	Failed    int     `json:"failed"`
	DurationS float64 `json:"duration_s"`
	Checks    []Check `json:"checks"`
	Error     string  `json:"error,omitempty"`
}

// Check is a single assertion within a canary run.
type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// Runner accumulates check results for a single canary scenario.
type Runner struct {
	scenario string
	sdk      string
	start    time.Time
	checks   []Check
	passed   int
	failed   int
}

// NewRunner creates a Runner for the given scenario and SDK label.
func NewRunner(scenario, sdk string) *Runner {
	return &Runner{scenario: scenario, sdk: sdk, start: time.Now()}
}

// Assert records a named check. If cond is false, detail is included.
func (r *Runner) Assert(cond bool, name, detail string) {
	c := Check{Name: name, Passed: cond, Detail: detail}
	r.checks = append(r.checks, c)
	if cond {
		r.passed++
	} else {
		r.failed++
	}
}

// OK records a passing check with no detail.
func (r *Runner) OK(name string) { r.Assert(true, name, "") }

// Fail records a failing check.
func (r *Runner) Fail(name, detail string) { r.Assert(false, name, detail) }

// AssertNoError records passing if err is nil, failing otherwise.
// Returns true if the assertion passed (no error).
func (r *Runner) AssertNoError(err error, name string) bool {
	if err != nil {
		r.Fail(name, err.Error())
		return false
	}
	r.OK(name)
	return true
}

// AssertError records passing if err is non-nil (expected error path).
func (r *Runner) AssertError(err error, name string) {
	if err == nil {
		r.Fail(name, "expected an error but got none")
	} else {
		r.Assert(true, name, err.Error())
	}
}

// Result returns the accumulated result.
func (r *Runner) Result() Result {
	return Result{
		Scenario:  r.scenario,
		SDK:       r.sdk,
		Passed:    r.passed,
		Failed:    r.failed,
		DurationS: time.Since(r.start).Seconds(),
		Checks:    r.checks,
	}
}

// WriteHTTP writes the result as JSON with 200 (all passed) or 500 (any failed).
func (r *Runner) WriteHTTP(w http.ResponseWriter) {
	res := r.Result()
	status := http.StatusOK
	if res.Failed > 0 {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(res)
}

// Print writes a human-readable summary to stdout and returns the result.
func (r *Runner) Print() Result {
	res := r.Result()
	fmt.Printf("=== Canary: %s / %s ===\n", res.SDK, res.Scenario)
	for _, c := range res.Checks {
		if c.Passed {
			fmt.Printf("  PASS %s\n", c.Name)
		} else {
			fmt.Printf("  FAIL %s: %s\n", c.Name, c.Detail)
		}
	}
	fmt.Printf("--- %d passed, %d failed in %.2fs ---\n\n", res.Passed, res.Failed, res.DurationS)
	return res
}

// ErrDetail returns err.Error() or fallback if err is nil.
func ErrDetail(err error, fallback string) string {
	if err != nil {
		return err.Error()
	}
	return fallback
}

// RawDo performs a raw HTTP request against the API, returning status code and body.
// Used for checks that the SDK doesn't expose (e.g. SSE, verbose flag, raw error shapes).
func RawDo(ctx context.Context, method, url, apiKey string, body []byte) (int, []byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, respBody, err
}

// JSONGet performs a GET and decodes the JSON response into result.
func JSONGet(ctx context.Context, url, apiKey string, result any) (int, error) {
	status, body, err := RawDo(ctx, "GET", url, apiKey, nil)
	if err != nil {
		return status, err
	}
	if result != nil && len(body) > 0 {
		if err := json.Unmarshal(body, result); err != nil {
			return status, fmt.Errorf("decode response: %w (body: %.200s)", err, body)
		}
	}
	return status, nil
}

// HasErrorField returns true if a JSON body has an "error" string field.
func HasErrorField(body []byte) bool {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return false
	}
	v, ok := obj["error"]
	if !ok {
		return false
	}
	_, isStr := v.(string)
	return isStr
}

// HasField returns true if a JSON body has the named field (any type).
func HasField(body []byte, field string) bool {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return false
	}
	_, ok := obj[field]
	return ok
}

// FieldString returns a string field from JSON or "".
func FieldString(body []byte, field string) string {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return ""
	}
	s, _ := obj[field].(string)
	return s
}

// ContainsLeakedInternals returns true if the body contains Go runtime noise.
func ContainsLeakedInternals(body []byte) bool {
	s := strings.ToLower(string(body))
	for _, marker := range []string{"panic:", "runtime error:", "goroutine ", "stack trace"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}
