// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Command repolint runs the repository-layout lint checks defined in
// pkg/repolint against the canonical paths of this repo. It is invoked
// by .githooks/pre-commit and the Lint job in .github/workflows/ci.yml.
//
// Exit codes:
//
//	0 — all checks passed
//	1 — one or more checks failed (caller should NOT proceed)
//	2 — internal error (bad invocation, repo structure missing, etc.)
//
// Usage:
//
//	repolint                       # run all checks against the repo root
//	repolint -repo /path           # run checks against an alternate root
//	repolint -fix-drift            # also: copy api/migrations/ → charts/llmsafespace/migrations/
//	repolint -fix-worklogs         # also: auto-renumber duplicate worklog files, then run all checks
//	repolint -fix-worklogs-only    # ONLY auto-renumber; skip checks. For .githooks/post-rewrite
//	                               # where the tree may be mid-rebase and checks would be noisy.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/lenaxia/llmsafespace/pkg/repolint"
)

const (
	exitOK       = 0
	exitFailures = 1
	exitInternal = 2

	// worklogGrandfatherBelow is the cutoff before which historical
	// duplicates and gaps are tolerated. See pkg/repolint/sequence_test.go
	// (TestLive_Worklogs_NoCollisionsOrGaps) for the rationale.
	worklogGrandfatherBelow = 97
)

func main() {
	repoFlag := flag.String("repo", "", "repository root to lint (default: auto-detect from CWD)")
	fixDrift := flag.Bool("fix-drift", false, "copy api/migrations/*.sql into charts/llmsafespace/migrations/ to resolve drift")
	fixWorklogs := flag.Bool("fix-worklogs", false, "auto-renumber duplicate worklog files to the next available number, then run all checks")
	fixWorklogsOnly := flag.Bool("fix-worklogs-only", false, "auto-renumber duplicate worklog files and exit (no checks). Used by .githooks/post-rewrite.")
	flag.Parse()

	root, err := resolveRoot(*repoFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(exitInternal)
	}

	if *fixDrift {
		if err := syncChartMigrations(root); err != nil {
			fmt.Fprintf(os.Stderr, "fix-drift failed: %v\n", err)
			os.Exit(exitInternal)
		}
		fmt.Println("ok: synced charts/llmsafespace/migrations/ from api/migrations/")
	}

	// -fix-worklogs-only is the hook mode: do the rename pass and exit
	// without running checks. The post-rewrite hook fires after a rebase
	// (or --amend), at which point the working tree may not yet reflect
	// every replayed commit and the sequence checks would produce
	// confusing output. The pre-commit hook runs the full check separately
	// on the next commit.
	if *fixWorklogsOnly {
		renames, err := runFixWorklogs(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fix-worklogs failed: %v\n", err)
			os.Exit(exitInternal)
		}
		_ = renames // runFixWorklogs already printed
		os.Exit(exitOK)
	}

	if *fixWorklogs {
		if _, err := runFixWorklogs(root); err != nil {
			fmt.Fprintf(os.Stderr, "fix-worklogs failed: %v\n", err)
			os.Exit(exitInternal)
		}
	}

	failures := 0
	failures += runMigrations(root)
	failures += runWorklogs(root)
	failures += runWorklogMainline(root)
	failures += runChartDrift(root)
	failures += runCRDDrift(root)

	if failures > 0 {
		fmt.Fprintf(os.Stderr, "\nrepolint: %d check(s) failed\n", failures)
		os.Exit(exitFailures)
	}
	fmt.Println("repolint: all checks passed")
	os.Exit(exitOK)
}

// runFixWorklogs executes the worklog auto-renumber pass against
// <root>/worklogs and prints one "renamed X → Y" line per rename (or a
// "no duplicates found" line when clean). Returns the renames so callers
// can decide whether to re-run checks, stage, etc.
func runFixWorklogs(root string) ([]repolint.WorklogRename, error) {
	wlDir := filepath.Join(root, "worklogs")
	renames, err := repolint.FixWorklogs(wlDir)
	if err != nil {
		return nil, err
	}
	if len(renames) == 0 {
		fmt.Println("fix-worklogs: no duplicates found, nothing to rename")
	} else {
		for _, r := range renames {
			fmt.Printf("fix-worklogs: renamed %s → %s\n", r.From, r.To)
		}
	}
	return renames, nil
}

func resolveRoot(explicit string) (string, error) {
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for d := wd; d != "/" && d != "."; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
	}
	return "", fmt.Errorf("could not locate go.mod ancestor of %s", wd)
}

func runMigrations(root string) int {
	dir := filepath.Join(root, "api", "migrations")
	rep, err := repolint.SequenceCheck(repolint.SequenceConfig{
		Dir:           dir,
		Pattern:       repolint.MigrationPattern,
		RequirePaired: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL  migrations sequence: %v\n", err)
		return 1
	}
	if !rep.OK() {
		fmt.Fprintf(os.Stderr, "FAIL  migrations sequence in %s:\n%s\n", dir, rep.String())
		return 1
	}
	fmt.Printf("ok    migrations sequence (%d migrations, max version %d)\n",
		len(rep.SeenVersions), rep.MaxVersion)
	return 0
}

func runWorklogs(root string) int {
	dir := filepath.Join(root, "worklogs")
	rep, err := repolint.SequenceCheck(repolint.SequenceConfig{
		Dir:              dir,
		Pattern:          repolint.WorklogPattern,
		RequirePaired:    false,
		GrandfatherBelow: worklogGrandfatherBelow,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL  worklogs sequence: %v\n", err)
		return 1
	}
	if !rep.OK() {
		fmt.Fprintf(os.Stderr, "FAIL  worklogs sequence in %s (entries >= %04d must be unique and contiguous):\n%s\n",
			dir, worklogGrandfatherBelow, rep.String())
		return 1
	}
	fmt.Printf("ok    worklogs sequence (%d worklogs, max %04d, grandfathered <%04d)\n",
		len(rep.SeenVersions), rep.MaxVersion, worklogGrandfatherBelow)
	return 0
}

func runWorklogMainline(root string) int {
	dir := filepath.Join(root, "worklogs")
	rep, err := repolint.MainlineCheck(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN  worklogs mainline check skipped: %v\n", err)
		return 0
	}
	if !rep.OK() {
		fmt.Fprintf(os.Stderr, "FAIL  worklogs collide with origin/main:\n%s\n", rep.String())
		return 1
	}
	if rep.NextNumber > 0 {
		fmt.Printf("ok    worklogs no mainline collisions (next available: %04d)\n", rep.NextNumber)
	} else {
		fmt.Println("ok    worklogs no mainline collisions")
	}
	return 0
}

func runChartDrift(root string) int {
	canon := filepath.Join(root, "api", "migrations")
	mirror := filepath.Join(root, "charts", "llmsafespace", "migrations")
	rep, err := repolint.DriftCheck(repolint.DriftConfig{
		CanonicalDir: canon,
		MirrorDir:    mirror,
		Glob:         "*.sql",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL  chart-migrations drift: %v\n", err)
		return 1
	}
	if !rep.OK() {
		fmt.Fprintf(os.Stderr, "FAIL  chart-migrations drift between\n        canonical: %s\n        mirror:    %s\n%s\n  Fix with: make chart-sync-migrations  (or: repolint -fix-drift)\n",
			canon, mirror, rep.String())
		return 1
	}
	fmt.Println("ok    chart migrations match api/migrations/")
	return 0
}

// runCRDDrift compares each Go struct in repolint.LiveBindings against
// the corresponding chart CRD's openAPIV3Schema properties. Drift is
// reported per-binding so a multi-CRD failure surfaces every diff
// rather than stopping at the first one — the operator may want to
// fix them in a single edit pass.
//
// Originating incident: worklog 0118-0119 (2026-06-01) — the
// AgentSessionStatus.Status field landed in Go but the chart CRD
// still had `lastActivityAt: date-time`. apiserver dropped the field
// silently on every reconcile that wrote a session list. Symptom was
// invisible in tests because Go-side serialization succeeded; the
// drop happened on the wire.
func runCRDDrift(root string) int {
	bindings := repolint.LiveBindings()
	failed := 0
	for _, b := range bindings {
		rep, err := repolint.CRDDriftCheck(root, b)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL  CRD drift (%s :: %s): %v\n", b.GoFile, b.GoStruct, err)
			failed++
			continue
		}
		if !rep.OK() {
			fmt.Fprintf(os.Stderr, "FAIL  CRD drift:\n%s", rep.String())
			failed++
		}
	}
	if failed > 0 {
		fmt.Fprintln(os.Stderr,
			"  Fix: align the chart CRD's openAPIV3Schema.properties\n"+
				"  with the Go struct's JSON tags. See worklog 0119 and\n"+
				"  pkg/repolint/crd_drift.go for context.")
		return 1
	}
	fmt.Printf("ok    CRD drift (%d bindings checked)\n", len(bindings))
	return 0
}

// syncChartMigrations performs `cp -a api/migrations/*.sql charts/llmsafespace/migrations/`
// in pure Go. Pre-existing .sql files in the mirror that are no longer
// present in canonical are removed, so a rename in canonical surfaces
// correctly in the mirror.
func syncChartMigrations(root string) error {
	canon := filepath.Join(root, "api", "migrations")
	mirror := filepath.Join(root, "charts", "llmsafespace", "migrations")

	// Remove obsolete .sql files from the mirror.
	mirrorEntries, err := os.ReadDir(mirror)
	if err != nil {
		return fmt.Errorf("read mirror %s: %w", mirror, err)
	}
	canonNames := map[string]bool{}
	canonEntries, err := os.ReadDir(canon)
	if err != nil {
		return fmt.Errorf("read canonical %s: %w", canon, err)
	}
	for _, e := range canonEntries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			canonNames[e.Name()] = true
		}
	}
	for _, e := range mirrorEntries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		if !canonNames[e.Name()] {
			if err := os.Remove(filepath.Join(mirror, e.Name())); err != nil {
				return fmt.Errorf("remove stale %s: %w", e.Name(), err)
			}
		}
	}

	// Copy/overwrite every canonical .sql into the mirror.
	for name := range canonNames {
		if err := copyFile(filepath.Join(canon, name), filepath.Join(mirror, name)); err != nil {
			return fmt.Errorf("copy %s: %w", name, err)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
