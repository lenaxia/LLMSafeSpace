// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package gharouter exercises the GitHub Actions AI-command routing logic in
// .github/scripts/route-command.sh. The routing (command detection, NOTE
// extraction, --no-merge hold, prompt assembly) is non-trivial shell that a
// silent edit could break; this test invokes the script as the workflow does
// (source + call route_command) across every command and modifier so the
// routing surface has a persistent regression guard run by `make test` and CI.
package gharouter

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// scriptPath resolves .github/scripts/route-command.sh relative to the repo
// root. The test runs from the package dir (tests/gharouter), so walk up to
// find go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (go.mod not found)")
		}
		dir = parent
	}
}

// runRoute sources the script and calls route_command for one comment body,
// returning the resolved COMMAND, NOTE, HOLD_MERGE, and the assembled prompt.
func runRoute(t *testing.T, root, commentBody, prURL, eventName string) (command, note, holdMerge, prompt string) {
	t.Helper()
	scriptRel := filepath.Join(".github", "scripts", "route-command.sh")
	outFile := filepath.Join(t.TempDir(), "prompt.txt")
	// Source the script (as the workflow does), call route_command, then print
	// the resolved vars. Running via `bash -c` keeps the shell as the single
	// source of truth — no logic is duplicated in Go.
	harness := `set -euo pipefail
source "$1"
OUT_FILE="$2" route_command
printf '__CMD=%s\n__NOTE=%s\n__HOLD=%s\n' "$COMMAND" "$NOTE" "$HOLD_MERGE"
`
	cmd := exec.Command("bash", "-c", harness, "gharouter-harness", filepath.Join(root, scriptRel), outFile)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"COMMENT_BODY="+commentBody,
		"PR_URL="+prURL,
		"EVENT_NAME="+eventName,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("route-command.sh failed for %q: %v\noutput:\n%s", commentBody, err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "__CMD="):
			command = strings.TrimPrefix(line, "__CMD=")
		case strings.HasPrefix(line, "__NOTE="):
			note = strings.TrimPrefix(line, "__NOTE=")
		case strings.HasPrefix(line, "__HOLD="):
			holdMerge = strings.TrimPrefix(line, "__HOLD=")
		}
	}
	promptBytes, err := os.ReadFile(outFile)
	require.NoError(t, err)
	prompt = string(promptBytes)
	return command, note, holdMerge, prompt
}

// holdDirectiveMarker is the substring unique to the INJECTED --no-merge hold
// directive (vs design.md's own rule-7 text, which also contains "MERGE HOLD").
// Using this avoids false-positive "lacks MERGE HOLD" assertions on /design.
const holdDirectiveMarker = "The collaborator passed `--no-merge`"

// routeCase is one routing assertion. noteOK/holdOK/promptChecks let a case
// assert a subset of the outputs (e.g. skip NOTE for commands that ignore it).
type routeCase struct {
	name            string
	body            string
	prURL           string
	event           string
	wantCommand     string
	wantNote        string // asserted only when noteAny is false
	noteAny         bool   // do not assert NOTE (e.g. /merge, /ai-fallback)
	wantHold        string // "0" or "1"
	promptHas       []string
	promptLacksHold bool // assert the injected --no-merge hold directive is absent
	promptHasHold   bool // assert the injected --no-merge hold directive is present
}

func TestRouteCommand(t *testing.T) {
	root := repoRoot(t)

	cases := []routeCase{
		{name: "design standalone", body: "/design multi-cloud relay routing",
			wantCommand: "/design", wantNote: "multi-cloud relay routing", wantHold: "0",
			promptHas: []string{"design document", "Code Change Workflow"}, promptLacksHold: true},
		{name: "design inline", body: "Please /design the auth refactor",
			wantCommand: "/design", wantNote: "the auth refactor", wantHold: "0"},
		{name: "merge standalone", body: "/merge",
			wantCommand: "/merge", noteAny: true, wantHold: "0",
			promptHas: []string{"squash"}, promptLacksHold: true},
		{name: "merge inline", body: "looks good, /merge it",
			wantCommand: "/merge", noteAny: true, wantHold: "0"},
		{name: "review", body: "/review focus on auth",
			wantCommand: "/review", wantNote: "focus on auth", wantHold: "0"},
		{name: "fix basic", body: "/fix null pointer in reconciler",
			wantCommand: "/fix", wantNote: "null pointer in reconciler", wantHold: "0"},
		{name: "implement basic", body: "/implement add rate-limit cache",
			wantCommand: "/implement", wantNote: "add rate-limit cache", wantHold: "0"},
		{name: "test basic", body: "/test pkg/secrets",
			wantCommand: "/test", wantNote: "pkg/secrets", wantHold: "0"},
		{name: "security basic", body: "/security check RBAC",
			wantCommand: "/security", wantNote: "check RBAC", wantHold: "0"},
		{name: "analyze basic", body: "/analyze the relay path",
			wantCommand: "/analyze", wantNote: "the relay path", wantHold: "0"},
		{name: "explain basic", body: "/explain the CRD ownership split",
			wantCommand: "/explain", wantNote: "the CRD ownership split", wantHold: "0"},
		{name: "triage basic", body: "/triage this issue",
			wantCommand: "/triage", wantNote: "this issue", wantHold: "0"},
		{name: "help basic", body: "/help",
			wantCommand: "/help", wantNote: "", wantHold: "0"},
		{name: "ai with note", body: "/ai can you check the logs",
			wantCommand: "/ai", wantNote: "can you check the logs", wantHold: "0"},
		{name: "ai on PR (no note) -> pr-review", body: "/ai", prURL: "https://api.github.com/x/y",
			wantCommand: "/ai", noteAny: true, wantHold: "0",
			promptHas: []string{"re-review"}},
		{name: "ai on issue (no note) -> issue-responder", body: "/ai", event: "issue_comment",
			wantCommand: "/ai", noteAny: true, wantHold: "0",
			promptHas: []string{"Analyze the full issue thread"}},

		// --- prefix safety: /testing must NOT route to /test ---
		{name: "prefix safety /testing", body: "/testing the waters",
			wantCommand: "/ai", noteAny: true, wantHold: "0"},
		{name: "prefix safety /fixing", body: "/fixing a thing",
			wantCommand: "/ai", noteAny: true, wantHold: "0"},
		{name: "prefix safety /implementing", body: "/implementing now",
			wantCommand: "/ai", noteAny: true, wantHold: "0"},

		// --- --no-merge: TRAILING ONLY ---
		{name: "no-merge trailing on implement", body: "/implement add rate-limit cache --no-merge",
			wantCommand: "/implement", wantNote: "add rate-limit cache", wantHold: "1",
			promptHasHold: true},
		{name: "no-merge trailing on fix", body: "/fix null pointer --no-merge",
			wantCommand: "/fix", wantNote: "null pointer", wantHold: "1",
			promptHasHold: true},
		{name: "no-merge trailing on test", body: "/test pkg/secrets --no-merge",
			wantCommand: "/test", wantNote: "pkg/secrets", wantHold: "1",
			promptHasHold: true},
		{name: "no-merge trailing on security", body: "/security --no-merge",
			wantCommand: "/security", wantNote: "", wantHold: "1",
			promptHasHold: true},
		// leading position is NOT the flag (treated as description); avoids the
		// self-referential false positive the substring matcher hit.
		{name: "no-merge leading NOT flag (implement)", body: "/implement --no-merge add cache",
			wantCommand: "/implement", wantNote: "--no-merge add cache", wantHold: "0",
			promptLacksHold: true},
		// mid-description literal must NOT hold and must NOT be mangled
		{name: "no-merge mid-description NOT flag", body: "/fix the --no-merge stripping is greedy",
			wantCommand: "/fix", wantNote: "the --no-merge stripping is greedy", wantHold: "0",
			promptLacksHold: true},
		// /design holds via its prompt even without the flag; flag stripped but ignored
		{name: "no-merge on design stripped ignored", body: "/design foo --no-merge",
			wantCommand: "/design", wantNote: "foo", wantHold: "0",
			promptHas: []string{"design document"}, promptLacksHold: true},
		// /merge ignores the flag entirely (finalize-only)
		{name: "no-merge on merge ignored", body: "/merge --no-merge",
			wantCommand: "/merge", noteAny: true, wantHold: "0",
			promptLacksHold: true},
		// trailing whitespace after the flag still counts
		{name: "no-merge trailing whitespace", body: "/implement add cache --no-merge   ",
			wantCommand: "/implement", wantNote: "add cache", wantHold: "1",
			promptHasHold: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, note, hold, prompt := runRoute(t, root, tc.body, tc.prURL, tc.event)
			require.Equal(t, tc.wantCommand, cmd, "COMMAND for body %q", tc.body)
			if !tc.noteAny {
				require.Equal(t, tc.wantNote, note, "NOTE for body %q", tc.body)
			}
			require.Equal(t, tc.wantHold, hold, "HOLD_MERGE for body %q", tc.body)
			for _, want := range tc.promptHas {
				require.Contains(t, prompt, want, "prompt should contain %q for body %q", want, tc.body)
			}
			if tc.promptHasHold {
				require.Contains(t, prompt, holdDirectiveMarker, "prompt should contain injected hold directive for body %q", tc.body)
			}
			if tc.promptLacksHold {
				require.NotContains(t, prompt, holdDirectiveMarker, "prompt should NOT contain injected hold directive for body %q", tc.body)
			}
		})
	}
}

// TestRouteCommandPromptAssembly verifies the prompt is always headed by
// context.md + core-rules.md regardless of command (the shared header).
func TestRouteCommandPromptAssembly(t *testing.T) {
	root := repoRoot(t)
	for _, body := range []string{"/review", "/fix x", "/design y", "/merge", "/help"} {
		_, _, _, prompt := runRoute(t, root, body, "", "issue_comment")
		require.True(t, strings.Contains(prompt, "Repository: LLMSafeSpaces"),
			"prompt for %q missing context.md header", body)
		require.True(t, strings.Contains(prompt, "## Core Rules"),
			"prompt for %q missing core-rules.md header", body)
	}
}
