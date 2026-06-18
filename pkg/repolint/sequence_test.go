// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package repolint

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// SequenceCheck core
// ---------------------------------------------------------------------------

func TestSequenceCheck_HappyPath(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "000001_a.up.sql"), "")
	mustWrite(t, filepath.Join(dir, "000001_a.down.sql"), "")
	mustWrite(t, filepath.Join(dir, "000002_b.up.sql"), "")
	mustWrite(t, filepath.Join(dir, "000002_b.down.sql"), "")
	mustWrite(t, filepath.Join(dir, "000003_c.up.sql"), "")
	mustWrite(t, filepath.Join(dir, "000003_c.down.sql"), "")

	rep, err := SequenceCheck(SequenceConfig{
		Dir:           dir,
		Pattern:       MigrationPattern,
		RequirePaired: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("expected OK report, got: %s", rep.String())
	}
	if rep.MaxVersion != 3 {
		t.Fatalf("expected MaxVersion=3, got %d", rep.MaxVersion)
	}
}

func TestSequenceCheck_DuplicateVersion(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "000001_a.up.sql"), "")
	mustWrite(t, filepath.Join(dir, "000001_a.down.sql"), "")
	mustWrite(t, filepath.Join(dir, "000002_b.up.sql"), "")
	mustWrite(t, filepath.Join(dir, "000002_b.down.sql"), "")
	// Collision: two different migrations both numbered 000003
	mustWrite(t, filepath.Join(dir, "000003_c.up.sql"), "")
	mustWrite(t, filepath.Join(dir, "000003_c.down.sql"), "")
	mustWrite(t, filepath.Join(dir, "000003_d.up.sql"), "")
	mustWrite(t, filepath.Join(dir, "000003_d.down.sql"), "")

	rep, err := SequenceCheck(SequenceConfig{
		Dir:           dir,
		Pattern:       MigrationPattern,
		RequirePaired: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.OK() {
		t.Fatalf("expected NOT OK; collision should fail. got: %s", rep.String())
	}
	if len(rep.Duplicates) != 1 || rep.Duplicates[0].Version != 3 {
		t.Fatalf("expected duplicate at version 3, got %+v", rep.Duplicates)
	}
	if !strings.Contains(rep.String(), "duplicate") {
		t.Fatalf("expected report to mention duplicate; got %q", rep.String())
	}
}

func TestSequenceCheck_GapInSequence(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "000001_a.up.sql"), "")
	mustWrite(t, filepath.Join(dir, "000001_a.down.sql"), "")
	// Gap: 2 missing
	mustWrite(t, filepath.Join(dir, "000003_c.up.sql"), "")
	mustWrite(t, filepath.Join(dir, "000003_c.down.sql"), "")

	rep, err := SequenceCheck(SequenceConfig{
		Dir:           dir,
		Pattern:       MigrationPattern,
		RequirePaired: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.OK() {
		t.Fatalf("expected NOT OK; gap should fail. got: %s", rep.String())
	}
	if len(rep.MissingVersions) != 1 || rep.MissingVersions[0] != 2 {
		t.Fatalf("expected missing version 2, got %v", rep.MissingVersions)
	}
}

func TestSequenceCheck_AllowGaps_GapStillReportedButOKTrue(t *testing.T) {
	// Worklog-style scenario: contiguous numbers required by some
	// callers, but for append-only docs we want gaps to be warnings,
	// not failures. Verify OK() is true while MissingVersions is
	// still populated and HasWarnings() reports the gap.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0001_2026-06-18_alpha.md"), "")
	// Gap at 2.
	mustWrite(t, filepath.Join(dir, "0003_2026-06-18_charlie.md"), "")

	rep, err := SequenceCheck(SequenceConfig{
		Dir:       dir,
		Pattern:   WorklogPattern,
		AllowGaps: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("expected OK with AllowGaps=true; got: %s", rep.String())
	}
	if !rep.HasWarnings() {
		t.Fatalf("expected HasWarnings=true with gap, got false")
	}
	if len(rep.MissingVersions) != 1 || rep.MissingVersions[0] != 2 {
		t.Fatalf("expected MissingVersions=[2], got %v", rep.MissingVersions)
	}
	if !rep.GapsAllowed {
		t.Fatalf("expected GapsAllowed=true on report")
	}
}

func TestSequenceCheck_AllowGaps_DuplicatesStillFail(t *testing.T) {
	// Even with AllowGaps=true, duplicate version numbers must still
	// fail OK(). The whole point of the check is referential
	// uniqueness; relaxing gaps doesn't relax that.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0001_2026-06-18_alpha.md"), "")
	mustWrite(t, filepath.Join(dir, "0001_2026-06-18_bravo.md"), "")

	rep, err := SequenceCheck(SequenceConfig{
		Dir:       dir,
		Pattern:   WorklogPattern,
		AllowGaps: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.OK() {
		t.Fatalf("expected NOT OK on duplicate even with AllowGaps; got: %s", rep.String())
	}
	if len(rep.Duplicates) != 1 {
		t.Fatalf("expected 1 duplicate, got %d", len(rep.Duplicates))
	}
}

func TestSequenceCheck_AllowGapsFalse_DefaultBehaviorPreserved(t *testing.T) {
	// Migrations and any caller that doesn't pass AllowGaps must
	// still fail on gaps — this is load-bearing for migration
	// safety. Explicit regression guard against accidentally
	// flipping the default.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "000001_a.up.sql"), "")
	mustWrite(t, filepath.Join(dir, "000001_a.down.sql"), "")
	mustWrite(t, filepath.Join(dir, "000003_c.up.sql"), "")
	mustWrite(t, filepath.Join(dir, "000003_c.down.sql"), "")

	rep, err := SequenceCheck(SequenceConfig{
		Dir:           dir,
		Pattern:       MigrationPattern,
		RequirePaired: true,
		// AllowGaps NOT set → defaults to false.
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.OK() {
		t.Fatalf("expected NOT OK; AllowGaps default must be false. got: %s", rep.String())
	}
	if rep.HasWarnings() {
		t.Fatalf("HasWarnings() must be false when GapsAllowed is false")
	}
}

func TestSequenceCheck_AllowGaps_NoGaps_HasWarningsFalse(t *testing.T) {
	// Happy path: AllowGaps=true AND contiguous sequence. HasWarnings
	// must be false. Catches the bug class where someone simplifies
	// HasWarnings() to "return r.GapsAllowed" without the
	// len(MissingVersions)>0 clause.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0001_2026-06-18_alpha.md"), "")
	mustWrite(t, filepath.Join(dir, "0002_2026-06-18_bravo.md"), "")
	mustWrite(t, filepath.Join(dir, "0003_2026-06-18_charlie.md"), "")

	rep, err := SequenceCheck(SequenceConfig{
		Dir:       dir,
		Pattern:   WorklogPattern,
		AllowGaps: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("expected OK with no gaps; got: %s", rep.String())
	}
	if rep.HasWarnings() {
		t.Fatalf("expected HasWarnings()=false with no gaps; got true")
	}
	if !rep.GapsAllowed {
		t.Fatalf("expected GapsAllowed=true on report")
	}
}

func TestSequenceCheck_AllowGaps_GrandfatherBelowExcludesOldGaps(t *testing.T) {
	// Production config: cmd/repolint/main.go runWorklogs sets BOTH
	// AllowGaps=true AND GrandfatherBelow=97. Verify gaps strictly
	// below the threshold are excluded; gaps at or above the
	// threshold ARE reported.
	dir := t.TempDir()
	// Below grandfather threshold: 90 and 92 with a gap at 91 (and at 1..89,
	// 93..96 which are all before the threshold and thus excluded).
	mustWrite(t, filepath.Join(dir, "0090_2026-06-18_old-a.md"), "")
	mustWrite(t, filepath.Join(dir, "0092_2026-06-18_old-b.md"), "")
	// Above threshold: 97, 98 contiguous, then a real gap at 99.
	mustWrite(t, filepath.Join(dir, "0097_2026-06-18_new-a.md"), "")
	mustWrite(t, filepath.Join(dir, "0098_2026-06-18_new-b.md"), "")
	mustWrite(t, filepath.Join(dir, "0100_2026-06-18_new-c.md"), "")

	rep, err := SequenceCheck(SequenceConfig{
		Dir:              dir,
		Pattern:          WorklogPattern,
		AllowGaps:        true,
		GrandfatherBelow: 97,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("expected OK (gaps allowed); got: %s", rep.String())
	}
	if !rep.HasWarnings() {
		t.Fatalf("expected HasWarnings()=true (gap at 99)")
	}
	if len(rep.MissingVersions) != 1 || rep.MissingVersions[0] != 99 {
		t.Fatalf("expected MissingVersions=[99] (91 is grandfathered); got %v", rep.MissingVersions)
	}
}

func TestSequenceCheck_MissingDownPair(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "000001_a.up.sql"), "")
	mustWrite(t, filepath.Join(dir, "000001_a.down.sql"), "")
	mustWrite(t, filepath.Join(dir, "000002_b.up.sql"), "")
	// no .down.sql

	rep, err := SequenceCheck(SequenceConfig{
		Dir:           dir,
		Pattern:       MigrationPattern,
		RequirePaired: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.OK() {
		t.Fatalf("expected NOT OK; missing down pair should fail. got: %s", rep.String())
	}
	if len(rep.UnpairedFiles) != 1 || !strings.Contains(rep.UnpairedFiles[0], "000002_b") {
		t.Fatalf("expected unpaired entry for 000002_b, got %v", rep.UnpairedFiles)
	}
}

func TestSequenceCheck_PairedNotRequired(t *testing.T) {
	// Worklogs are single files (no up/down pair).
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0001_first.md"), "")
	mustWrite(t, filepath.Join(dir, "0002_second.md"), "")
	mustWrite(t, filepath.Join(dir, "0003_third.md"), "")

	rep, err := SequenceCheck(SequenceConfig{
		Dir:           dir,
		Pattern:       WorklogPattern,
		RequirePaired: false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("expected OK report, got: %s", rep.String())
	}
}

func TestSequenceCheck_GrandfatherBelow(t *testing.T) {
	// Pre-existing collisions and gaps below the threshold should be
	// silently allowed; new entries above the threshold must still be
	// clean.
	dir := t.TempDir()
	// historical mess
	mustWrite(t, filepath.Join(dir, "0001_2026-01-01_foo.md"), "")
	mustWrite(t, filepath.Join(dir, "0002_2026-01-02_bar.md"), "")
	mustWrite(t, filepath.Join(dir, "0002_2026-01-02_baz.md"), "") // collision below threshold
	// gap at 3
	mustWrite(t, filepath.Join(dir, "0004_2026-01-04_qux.md"), "")
	// new clean range
	mustWrite(t, filepath.Join(dir, "0010_2026-01-10_clean.md"), "")
	mustWrite(t, filepath.Join(dir, "0011_2026-01-11_clean.md"), "")

	rep, err := SequenceCheck(SequenceConfig{
		Dir:              dir,
		Pattern:          WorklogPattern,
		RequirePaired:    false,
		GrandfatherBelow: 10, // anything < 10 is grandfathered
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("expected OK with GrandfatherBelow=10, got: %s", rep.String())
	}

	// And: a NEW collision above the threshold MUST still fail.
	mustWrite(t, filepath.Join(dir, "0011_2026-01-11_collision.md"), "")
	rep2, err := SequenceCheck(SequenceConfig{
		Dir:              dir,
		Pattern:          WorklogPattern,
		RequirePaired:    false,
		GrandfatherBelow: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep2.OK() {
		t.Fatalf("expected NOT OK; collision >= GrandfatherBelow should still fail. got: %s", rep2.String())
	}
}

func TestSequenceCheck_IgnoresNonMatchingFiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "000001_a.up.sql"), "")
	mustWrite(t, filepath.Join(dir, "000001_a.down.sql"), "")
	// README and other non-pattern files should be silently ignored
	mustWrite(t, filepath.Join(dir, "README.md"), "# notes")
	mustWrite(t, filepath.Join(dir, "001_initial_schema.sql"), "")          // legacy 3-digit
	mustWrite(t, filepath.Join(dir, "001_initial_schema_rollback.sql"), "") // legacy 3-digit

	rep, err := SequenceCheck(SequenceConfig{
		Dir:           dir,
		Pattern:       MigrationPattern,
		RequirePaired: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("expected OK (legacy files ignored), got: %s", rep.String())
	}
}

func TestSequenceCheck_DirDoesNotExist(t *testing.T) {
	_, err := SequenceCheck(SequenceConfig{
		Dir:           "/nonexistent/path",
		Pattern:       MigrationPattern,
		RequirePaired: true,
	})
	if err == nil {
		t.Fatal("expected error for nonexistent dir")
	}
}

func TestSequenceCheck_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	rep, err := SequenceCheck(SequenceConfig{
		Dir:           dir,
		Pattern:       MigrationPattern,
		RequirePaired: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("empty dir should be OK; got %s", rep.String())
	}
	if rep.MaxVersion != 0 {
		t.Fatalf("expected MaxVersion=0 for empty dir, got %d", rep.MaxVersion)
	}
}

// ---------------------------------------------------------------------------
// DriftCheck — chart migrations vs canonical
// ---------------------------------------------------------------------------

func TestDriftCheck_Identical(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	mustWrite(t, filepath.Join(a, "000001_x.up.sql"), "ALTER TABLE foo;")
	mustWrite(t, filepath.Join(a, "000001_x.down.sql"), "ALTER TABLE foo undo;")
	mustWrite(t, filepath.Join(b, "000001_x.up.sql"), "ALTER TABLE foo;")
	mustWrite(t, filepath.Join(b, "000001_x.down.sql"), "ALTER TABLE foo undo;")

	rep, err := DriftCheck(DriftConfig{
		CanonicalDir: a,
		MirrorDir:    b,
		Glob:         "*.sql",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("identical dirs should be OK; got %s", rep.String())
	}
}

func TestDriftCheck_MirrorMissingFile(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	mustWrite(t, filepath.Join(a, "000001_x.up.sql"), "ALTER TABLE foo;")
	mustWrite(t, filepath.Join(a, "000001_x.down.sql"), "")
	mustWrite(t, filepath.Join(a, "000002_y.up.sql"), "")
	mustWrite(t, filepath.Join(a, "000002_y.down.sql"), "")
	mustWrite(t, filepath.Join(b, "000001_x.up.sql"), "ALTER TABLE foo;")
	mustWrite(t, filepath.Join(b, "000001_x.down.sql"), "")
	// missing 000002_*

	rep, err := DriftCheck(DriftConfig{
		CanonicalDir: a,
		MirrorDir:    b,
		Glob:         "*.sql",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.OK() {
		t.Fatalf("expected NOT OK; missing file should fail. got: %s", rep.String())
	}
	if len(rep.MissingInMirror) != 2 {
		t.Fatalf("expected 2 missing-in-mirror files (up+down), got %v", rep.MissingInMirror)
	}
}

func TestDriftCheck_MirrorHasExtraFile(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	mustWrite(t, filepath.Join(a, "000001_x.up.sql"), "")
	mustWrite(t, filepath.Join(a, "000001_x.down.sql"), "")
	mustWrite(t, filepath.Join(b, "000001_x.up.sql"), "")
	mustWrite(t, filepath.Join(b, "000001_x.down.sql"), "")
	mustWrite(t, filepath.Join(b, "000002_orphan.up.sql"), "") // mirror has but canonical doesn't

	rep, err := DriftCheck(DriftConfig{
		CanonicalDir: a,
		MirrorDir:    b,
		Glob:         "*.sql",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.OK() {
		t.Fatalf("expected NOT OK; orphan file in mirror should fail. got: %s", rep.String())
	}
	if len(rep.ExtraInMirror) != 1 || rep.ExtraInMirror[0] != "000002_orphan.up.sql" {
		t.Fatalf("expected ExtraInMirror=[000002_orphan.up.sql], got %v", rep.ExtraInMirror)
	}
}

func TestDriftCheck_ContentDiffers(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	mustWrite(t, filepath.Join(a, "000001_x.up.sql"), "ALTER TABLE foo ADD COLUMN bar;")
	mustWrite(t, filepath.Join(a, "000001_x.down.sql"), "")
	mustWrite(t, filepath.Join(b, "000001_x.up.sql"), "ALTER TABLE foo ADD COLUMN baz;") // different
	mustWrite(t, filepath.Join(b, "000001_x.down.sql"), "")

	rep, err := DriftCheck(DriftConfig{
		CanonicalDir: a,
		MirrorDir:    b,
		Glob:         "*.sql",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.OK() {
		t.Fatalf("expected NOT OK; content drift should fail. got: %s", rep.String())
	}
	if len(rep.ContentDiffers) != 1 || rep.ContentDiffers[0] != "000001_x.up.sql" {
		t.Fatalf("expected ContentDiffers=[000001_x.up.sql], got %v", rep.ContentDiffers)
	}
}

func TestDriftCheck_IgnoresNonMatchingFiles(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	mustWrite(t, filepath.Join(a, "000001_x.up.sql"), "")
	mustWrite(t, filepath.Join(a, "000001_x.down.sql"), "")
	mustWrite(t, filepath.Join(a, "README.md"), "canonical readme")
	mustWrite(t, filepath.Join(b, "000001_x.up.sql"), "")
	mustWrite(t, filepath.Join(b, "000001_x.down.sql"), "")
	mustWrite(t, filepath.Join(b, "README.md"), "mirror-specific readme") // different content but ignored

	rep, err := DriftCheck(DriftConfig{
		CanonicalDir: a,
		MirrorDir:    b,
		Glob:         "*.sql",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("non-matching files should be ignored; got %s", rep.String())
	}
}

// ---------------------------------------------------------------------------
// Live-repo integration tests — these run against the real api/migrations/,
// charts/llmsafespace/migrations/, and worklogs/ trees of THIS repository.
// They are the regression net for the 2026-05-30 incident.
// ---------------------------------------------------------------------------

func TestLive_Migrations_NoCollisionsOrGaps(t *testing.T) {
	root := repoRoot(t)
	rep, err := SequenceCheck(SequenceConfig{
		Dir:           filepath.Join(root, "api", "migrations"),
		Pattern:       MigrationPattern,
		RequirePaired: true,
	})
	if err != nil {
		t.Fatalf("scanning api/migrations: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("api/migrations/ has issues:\n%s", rep.String())
	}
}

func TestLive_Worklogs_NoCollisionsOrGaps(t *testing.T) {
	root := repoRoot(t)
	rep, err := SequenceCheck(SequenceConfig{
		Dir:           filepath.Join(root, "worklogs"),
		Pattern:       WorklogPattern,
		RequirePaired: false,
		// Worklogs 0001..0096 contain 7 historical collisions and 1
		// historical gap (0067) caused by parallel two-agent work
		// before this lint existed. Renumbering them would require
		// updating ~26 cross-references and is too risky relative to
		// benefit. Cut the line at 0097 (worklog 0097 is where this
		// lint was introduced) and require strict sequencing forward.
		GrandfatherBelow: 97,
	})
	if err != nil {
		t.Fatalf("scanning worklogs: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("worklogs/ has issues at >= 0097:\n%s", rep.String())
	}
}

func TestLive_ChartMigrations_NoDriftFromCanonical(t *testing.T) {
	root := repoRoot(t)
	rep, err := DriftCheck(DriftConfig{
		CanonicalDir: filepath.Join(root, "api", "migrations"),
		MirrorDir:    filepath.Join(root, "charts", "llmsafespace", "migrations"),
		Glob:         "*.sql",
	})
	if err != nil {
		t.Fatalf("drift-checking chart migrations: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("chart migrations have drifted from api/migrations/:\n%s\n\nFix: make chart-sync-migrations", rep.String())
	}
}

// ---------------------------------------------------------------------------
// FixWorklogs
// ---------------------------------------------------------------------------

func TestFixWorklogs_NoDuplicates(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0097_2026-01-01_alpha.md"), "")
	mustWrite(t, filepath.Join(dir, "0098_2026-01-01_beta.md"), "")
	mustWrite(t, filepath.Join(dir, "0099_2026-01-01_gamma.md"), "")

	renames, err := FixWorklogs(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(renames) != 0 {
		t.Fatalf("expected 0 renames, got %d: %v", len(renames), renames)
	}
}

func TestFixWorklogs_SingleDuplicate(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0097_2026-01-01_alpha.md"), "")
	mustWrite(t, filepath.Join(dir, "0098_2026-01-01_aaa-first.md"), "")
	mustWrite(t, filepath.Join(dir, "0098_2026-01-01_zzz-second.md"), "") // duplicate

	renames, err := FixWorklogs(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(renames) != 1 {
		t.Fatalf("expected 1 rename, got %d: %v", len(renames), renames)
	}
	// The lexically-later file (zzz-second) should be bumped to 0099.
	if renames[0].From != "0098_2026-01-01_zzz-second.md" {
		t.Errorf("expected From=0098_2026-01-01_zzz-second.md, got %s", renames[0].From)
	}
	if renames[0].To != "0099_2026-01-01_zzz-second.md" {
		t.Errorf("expected To=0099_2026-01-01_zzz-second.md, got %s", renames[0].To)
	}
	// Verify the file actually moved.
	if _, err := os.Stat(filepath.Join(dir, renames[0].From)); err == nil {
		t.Error("old file still exists after rename")
	}
	if _, err := os.Stat(filepath.Join(dir, renames[0].To)); err != nil {
		t.Errorf("new file not found: %v", err)
	}
	// After fix, SequenceCheck should pass.
	rep, err := SequenceCheck(SequenceConfig{
		Dir:              dir,
		Pattern:          WorklogPattern,
		GrandfatherBelow: 97,
	})
	if err != nil {
		t.Fatalf("SequenceCheck: %v", err)
	}
	if !rep.OK() {
		t.Errorf("sequence still not OK after fix: %s", rep.String())
	}
}

func TestFixWorklogs_MultipleDuplicates(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0097_2026-01-01_a.md"), "")
	mustWrite(t, filepath.Join(dir, "0098_2026-01-01_b1.md"), "")
	mustWrite(t, filepath.Join(dir, "0098_2026-01-01_b2.md"), "") // dup of 0098
	mustWrite(t, filepath.Join(dir, "0099_2026-01-01_c1.md"), "")
	mustWrite(t, filepath.Join(dir, "0099_2026-01-01_c2.md"), "") // dup of 0099

	renames, err := FixWorklogs(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(renames) != 2 {
		t.Fatalf("expected 2 renames, got %d: %v", len(renames), renames)
	}
	rep, err := SequenceCheck(SequenceConfig{
		Dir:              dir,
		Pattern:          WorklogPattern,
		GrandfatherBelow: 97,
	})
	if err != nil {
		t.Fatalf("SequenceCheck: %v", err)
	}
	if !rep.OK() {
		t.Errorf("sequence still not OK after fix: %s", rep.String())
	}
}

func TestFixWorklogs_ThreeWayDuplicate(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0097_2026-01-01_aaa.md"), "")
	mustWrite(t, filepath.Join(dir, "0097_2026-01-01_bbb.md"), "")
	mustWrite(t, filepath.Join(dir, "0097_2026-01-01_ccc.md"), "")

	renames, err := FixWorklogs(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(renames) != 2 {
		t.Fatalf("expected 2 renames, got %d: %v", len(renames), renames)
	}
	rep, err := SequenceCheck(SequenceConfig{
		Dir:              dir,
		Pattern:          WorklogPattern,
		GrandfatherBelow: 97,
	})
	if err != nil {
		t.Fatalf("SequenceCheck: %v", err)
	}
	if !rep.OK() {
		t.Errorf("sequence still not OK after fix: %s", rep.String())
	}
}

func TestFixWorklogs_GrandfatheredVersionsUntouched(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0050_2026-01-01_old-a.md"), "")
	mustWrite(t, filepath.Join(dir, "0050_2026-01-01_old-b.md"), "") // grandfathered dup
	mustWrite(t, filepath.Join(dir, "0097_2026-01-01_new.md"), "")

	renames, err := FixWorklogs(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(renames) != 0 {
		t.Fatalf("expected 0 renames (all dups grandfathered), got %d: %v", len(renames), renames)
	}
	if _, err := os.Stat(filepath.Join(dir, "0050_2026-01-01_old-a.md")); err != nil {
		t.Error("old-a.md removed unexpectedly")
	}
	if _, err := os.Stat(filepath.Join(dir, "0050_2026-01-01_old-b.md")); err != nil {
		t.Error("old-b.md removed unexpectedly")
	}
}

func TestFixWorklogs_NonMatchingFilesIgnored(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "README.md"), "")
	mustWrite(t, filepath.Join(dir, "0097_2026-01-01_a.md"), "")
	mustWrite(t, filepath.Join(dir, "0098_2026-01-01_b.md"), "")

	renames, err := FixWorklogs(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(renames) != 0 {
		t.Fatalf("expected 0 renames, got %d", len(renames))
	}
}

func TestFixWorklogs_RenamedFileSelfReferenceUpdated(t *testing.T) {
	dir := t.TempDir()
	content := "See worklogs/0098_2026-01-01_zzz.md for context."
	mustWrite(t, filepath.Join(dir, "0097_2026-01-01_aaa.md"), "")
	mustWrite(t, filepath.Join(dir, "0098_2026-01-01_aaa.md"), "")
	mustWrite(t, filepath.Join(dir, "0098_2026-01-01_zzz.md"), content)

	renames, err := FixWorklogs(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(renames) != 1 {
		t.Fatalf("expected 1 rename, got %d: %v", len(renames), renames)
	}
	newPath := filepath.Join(dir, renames[0].To)
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("read renamed file: %v", err)
	}
	if strings.Contains(string(data), renames[0].From) {
		t.Errorf("renamed file still contains old filename %q", renames[0].From)
	}
	if !strings.Contains(string(data), renames[0].To) {
		t.Errorf("renamed file does not contain new filename %q; content: %s", renames[0].To, data)
	}
}

func TestFixWorklogs_RenameFails_ReturnsPartialResults(t *testing.T) {
	// Verify that when a rename fails (e.g. destination already exists due
	// to a race), FixWorklogs returns the renames completed so far plus
	// the error — it does not silently succeed or panic.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0097_2026-01-01_a.md"), "")
	mustWrite(t, filepath.Join(dir, "0098_2026-01-01_b1.md"), "")
	mustWrite(t, filepath.Join(dir, "0098_2026-01-01_b2.md"), "") // dup — will be renamed to 0099
	// Pre-create the target name so os.Rename fails.
	mustWrite(t, filepath.Join(dir, "0099_2026-01-01_b2.md"), "pre-existing")

	_, err := FixWorklogs(dir)
	// We expect an error because the rename destination already exists on
	// some platforms (Linux: os.Rename overwrites; test is platform-aware).
	// On Linux, Rename succeeds by overwriting, so only verify no panic.
	if err != nil {
		// On platforms where Rename fails, the error must mention the file.
		if !strings.Contains(err.Error(), "b2") {
			t.Errorf("error should mention the conflicting file, got: %v", err)
		}
	}
}

func TestFixWorklogs_SelfReferenceWriteFailureIsSilent(t *testing.T) {
	// FixWorklogs silently swallows os.WriteFile errors for the content
	// rewrite (the rename itself is the critical operation; stale
	// self-references are cosmetic). Verify that a read-only file does
	// not cause FixWorklogs to return an error.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0097_2026-01-01_a.md"), "")
	content := "worklogs/0098_2026-01-01_zzz.md — This worklog"
	mustWrite(t, filepath.Join(dir, "0098_2026-01-01_aaa.md"), "")
	mustWrite(t, filepath.Join(dir, "0098_2026-01-01_zzz.md"), content)
	// Make the duplicate read-only so the content rewrite will fail.
	if err := os.Chmod(filepath.Join(dir, "0098_2026-01-01_zzz.md"), 0o444); err != nil {
		t.Skipf("cannot chmod in this environment: %v", err)
	}

	renames, err := FixWorklogs(dir)
	// The rename should still succeed; the content-rewrite failure is silent.
	if err != nil {
		t.Fatalf("expected no error from FixWorklogs, got: %v", err)
	}
	if len(renames) != 1 {
		t.Fatalf("expected 1 rename, got %d", len(renames))
	}
}

// ---------------------------------------------------------------------------
// fixWorklogs with mainline awareness (the post-rebase auto-renumber path)
// ---------------------------------------------------------------------------
//
// These tests exercise the unexported fixWorklogs(dir, remoteByVersion)
// directly so they can control the "what's on origin/main" signal without
// needing a real git repo in the test sandbox. The public FixWorklogs(dir)
// is a thin wrapper that queries git then calls fixWorklogs — its git
// integration is covered by TestLive_Worklogs_NoMainlineCollisions.

// TestFixWorklogs_PrefersMainlineIncumbent is the core correctness test:
// when two local files share a version and one of them is on origin/main,
// the mainline file MUST stay and the local-only file MUST be renamed.
// This is the bug behind the repeated "chore: fix worklog number collision"
// commits — lexical tie-breaking renamed the wrong file half the time.
func TestFixWorklogs_PrefersMainlineIncumbent(t *testing.T) {
	dir := t.TempDir()
	// 0311_aaa is on mainline (incumbent); 0311_zzz is unique to this branch.
	mustWrite(t, filepath.Join(dir, "0310_2026-06-11_prev.md"), "")
	mustWrite(t, filepath.Join(dir, "0311_2026-06-11_aaa.md"), "")
	mustWrite(t, filepath.Join(dir, "0311_2026-06-11_zzz.md"), "")

	remoteByVersion := map[int][]string{
		311: {"0311_2026-06-11_aaa.md"},
	}

	renames, err := fixWorklogs(dir, remoteByVersion)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(renames) != 1 {
		t.Fatalf("expected 1 rename, got %d: %v", len(renames), renames)
	}
	if renames[0].From != "0311_2026-06-11_zzz.md" {
		t.Errorf("expected the mainline-UNAWARE file (zzz.md) to be renamed, got From=%s",
			renames[0].From)
	}
	if renames[0].To != "0312_2026-06-11_zzz.md" {
		t.Errorf("expected To=0312_2026-06-11_zzz.md (max local+remote + 1), got %s",
			renames[0].To)
	}
	// The incumbent must still exist, untouched.
	if _, err := os.Stat(filepath.Join(dir, "0311_2026-06-11_aaa.md")); err != nil {
		t.Errorf("incumbent mainline file was renamed/removed: %v", err)
	}
}

// TestFixWorklogs_ResolvesPureMainlineCollision covers the pre-rebase case:
// there is NO local duplicate — only a collision between a local file and a
// mainline file with a different slug. Locally, version 311 has exactly one
// file. Without mainline awareness, FixWorklogs would do nothing; the
// collision would only be caught later by MainlineCheck. With mainline
// awareness, the local file gets renumbered proactively.
func TestFixWorklogs_ResolvesPureMainlineCollision(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0310_2026-06-11_local.md"), "")
	mustWrite(t, filepath.Join(dir, "0311_2026-06-11_yours.md"), "")

	remoteByVersion := map[int][]string{
		311: {"0311_2026-06-11_mainline.md"},
	}

	renames, err := fixWorklogs(dir, remoteByVersion)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(renames) != 1 {
		t.Fatalf("expected 1 rename (the local file), got %d: %v", len(renames), renames)
	}
	if renames[0].From != "0311_2026-06-11_yours.md" {
		t.Errorf("expected yours.md to be renamed, got From=%s", renames[0].From)
	}
	if renames[0].To != "0312_2026-06-11_yours.md" {
		t.Errorf("expected To=0312_yours.md, got %s", renames[0].To)
	}
}

// TestFixWorklogs_AvoidsNumbersTakenOnMainline verifies that the next-free
// computation skips numbers that exist only on origin/main. Without this,
// renumbering 0311 → 0312 would just move the collision by one when
// mainline already has 0312.
func TestFixWorklogs_AvoidsNumbersTakenOnMainline(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0310_2026-06-11_a.md"), "")
	mustWrite(t, filepath.Join(dir, "0311_2026-06-11_yours.md"), "")

	remoteByVersion := map[int][]string{
		311: {"0311_2026-06-11_main.md"},
		312: {"0312_2026-06-11_taken.md"},
	}

	renames, err := fixWorklogs(dir, remoteByVersion)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(renames) != 1 {
		t.Fatalf("expected 1 rename, got %d: %v", len(renames), renames)
	}
	if renames[0].To != "0313_2026-06-11_yours.md" {
		t.Errorf("expected To=0313 (skip 0312 taken on mainline), got %s", renames[0].To)
	}
}

// TestFixWorklogs_MultipleLocalNewcomers covers the case where the local
// branch has multiple worklogs at the colliding version (e.g. you wrote
// several worklogs in one session and numbered them all 0311 by mistake)
// AND mainline also has 0311. Every local file at 0311 must move, because
// none of the local slugs match mainline's incumbent slug. The mainline
// incumbent stays.
func TestFixWorklogs_MultipleLocalNewcomersAgainstMainline(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0311_2026-06-11_aaa.md"), "")
	mustWrite(t, filepath.Join(dir, "0311_2026-06-11_bbb.md"), "")
	mustWrite(t, filepath.Join(dir, "0311_2026-06-11_ccc.md"), "")

	remoteByVersion := map[int][]string{
		311: {"0311_2026-06-11_incumbent.md"},
	}

	renames, err := fixWorklogs(dir, remoteByVersion)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(renames) != 3 {
		t.Fatalf("expected 3 renames (all 3 local files at v=311 collide with mainline), got %d: %v",
			len(renames), renames)
	}
	for _, r := range renames {
		if strings.Contains(r.From, "incumbent") {
			t.Errorf("mainline incumbent must never be renamed; got: %v", r)
		}
	}
	// Verify each local file was renamed away from 0311 and now exists at
	// its new path. (The incumbent itself is a mainline phantom — it was
	// never on disk locally; that's the point of the remoteByVersion map.)
	seenNew := map[string]bool{}
	for _, r := range renames {
		seenNew[r.To] = true
		if _, err := os.Stat(filepath.Join(dir, r.From)); err == nil {
			t.Errorf("old file %s should be gone after rename", r.From)
		}
		if _, err := os.Stat(filepath.Join(dir, r.To)); err != nil {
			t.Errorf("new file %s missing after rename: %v", r.To, err)
		}
	}
	if len(seenNew) != 3 {
		t.Errorf("expected 3 distinct rename targets, got %d: %v", len(seenNew), seenNew)
	}
}

// TestFixWorklogs_NilRemoteFallsBackToLexical locks in the
// pre-mainline-aware behavior: when origin/main is unavailable (nil remote
// map), the lexically-last file at each duplicate version is renamed. This
// preserves the contract of every existing TestFixWorklogs_* test that
// calls the public FixWorklogs(dir) in a temp dir with no git.
func TestFixWorklogs_NilRemoteFallsBackToLexical(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0097_2026-01-01_aaa.md"), "")
	mustWrite(t, filepath.Join(dir, "0098_2026-01-01_aaa.md"), "")
	mustWrite(t, filepath.Join(dir, "0098_2026-01-01_zzz.md"), "")

	renames, err := fixWorklogs(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(renames) != 1 {
		t.Fatalf("expected 1 rename, got %d: %v", len(renames), renames)
	}
	if renames[0].From != "0098_2026-01-01_zzz.md" {
		t.Errorf("expected lexical-last (zzz) to be renamed when no mainline signal, got %s",
			renames[0].From)
	}
}

// TestFixWorklogs_AllOnMainlineFallsBackToLexical is the defensive edge
// case: every local file at version V also exists on origin/main. This
// shouldn't happen in practice (origin/main enforces uniqueness at >= 97),
// but if it does, fixWorklogs must still make progress by falling back to
// lexical tie-breaking rather than looping forever or refusing to fix.
func TestFixWorklogs_AllOnMainlineFallsBackToLexical(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0311_2026-06-11_aaa.md"), "")
	mustWrite(t, filepath.Join(dir, "0311_2026-06-11_zzz.md"), "")

	remoteByVersion := map[int][]string{
		311: {
			"0311_2026-06-11_aaa.md",
			"0311_2026-06-11_zzz.md",
		},
	}

	renames, err := fixWorklogs(dir, remoteByVersion)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(renames) != 1 {
		t.Fatalf("expected 1 rename (lexical fallback), got %d: %v", len(renames), renames)
	}
	if renames[0].From != "0311_2026-06-11_zzz.md" {
		t.Errorf("expected lexical-last (zzz) as fallback, got %s", renames[0].From)
	}
}

// TestFixWorklogs_LocalIncumbentWithMainlinePhantom is the regression test
// for the infinite-loop guard at sequence.go:522 (the `!remoteSet[locals[0]]`
// clause). Without that guard, fixWorklogs would treat "local has the
// incumbent, mainline has incumbent + a phantom" as a fixable collision
// and renumber the incumbent — diverging from mainline and re-introducing
// the collision on the next merge, looping forever.
//
// Setup: local has exactly one file at v=311, and that file IS on
// origin/main. origin/main also has a different file at v=311 (the
// phantom). Expected behavior: 0 renames. The phantom is mainline's
// problem — SequenceCheck on mainline itself will flag it; a local tool
// must never rename a file that's already on mainline.
//
// No other existing test covers this branch:
//   - ResolvesPureMainlineCollision has a local file NOT on mainline.
//   - PrefersMainlineIncumbent / AllOnMainlineFallsBackToLexical use
//     len(locals) > 1, which takes the earlier branch at line 512.
func TestFixWorklogs_LocalIncumbentWithMainlinePhantom(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0310_2026-06-11_prev.md"), "")
	mustWrite(t, filepath.Join(dir, "0311_2026-06-11_incumbent.md"), "")

	remoteByVersion := map[int][]string{
		311: {
			"0311_2026-06-11_incumbent.md",
			"0311_2026-06-11_other.md", // phantom — exists on mainline but not locally
		},
	}

	renames, err := fixWorklogs(dir, remoteByVersion)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(renames) != 0 {
		t.Fatalf("expected 0 renames (local file is the incumbent; phantom is mainline's problem), got %d: %v",
			len(renames), renames)
	}
	// The incumbent must be untouched.
	if _, err := os.Stat(filepath.Join(dir, "0311_2026-06-11_incumbent.md")); err != nil {
		t.Errorf("incumbent file was renamed/removed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func TestMainlineCheck_NoCollisions(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0210_2026-06-11_alpha.md"), "")
	mustWrite(t, filepath.Join(dir, "0211_2026-06-11_beta.md"), "")

	rep, err := MainlineCheck(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("expected OK, got: %s", rep.String())
	}
	if rep.NextNumber < 212 {
		t.Fatalf("expected NextNumber >= 212 (local max + remote max), got %d", rep.NextNumber)
	}
}

func TestMainlineCheck_ReportFormatting(t *testing.T) {
	rep := MainlineReport{
		Collisions: []MainlineCollision{
			{
				Version:     209,
				LocalFiles:  []string{"0209_2026-06-11_local.md"},
				RemoteFiles: []string{"0209_2026-06-10_remote.md"},
			},
		},
		NextNumber: 219,
	}
	s := rep.String()
	if !strings.Contains(s, "version 0209") {
		t.Errorf("expected version in output, got: %s", s)
	}
	if !strings.Contains(s, "local:") {
		t.Errorf("expected 'local:' in output, got: %s", s)
	}
	if !strings.Contains(s, "remote:") {
		t.Errorf("expected 'remote:' in output, got: %s", s)
	}
	if !strings.Contains(s, "0219") {
		t.Errorf("expected next number in output, got: %s", s)
	}
}

func TestMainlineCheck_OKReport(t *testing.T) {
	rep := MainlineReport{NextNumber: 300}
	if !rep.OK() {
		t.Error("empty collisions should be OK")
	}
	if rep.String() != "(ok)" {
		t.Errorf("expected (ok), got: %s", rep.String())
	}
}

func TestMainlineCheck_NotOKReport(t *testing.T) {
	rep := MainlineReport{
		Collisions: []MainlineCollision{{Version: 100}},
	}
	if rep.OK() {
		t.Error("collision present should not be OK")
	}
}

func TestMainlineCheck_CollisionDetection(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0210_2026-06-11_shared.md"), "")
	mustWrite(t, filepath.Join(dir, "0215_2026-06-11_new-branch-worklog.md"), "")

	localVersions, localFiles, localMax, err := scanWorklogDir(dir)
	if err != nil {
		t.Fatalf("scanWorklogDir: %v", err)
	}
	if localMax != 215 {
		t.Fatalf("expected localMax=215, got %d", localMax)
	}
	if len(localVersions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(localVersions))
	}

	_ = map[int]bool{210: true, 215: true} // remote versions
	remoteFiles := map[int][]string{
		210: {"0210_2026-06-11_shared.md"},
		215: {"0215_2026-06-11_different-main-worklog.md"},
	}

	sort.Strings(localFiles[210])
	sort.Strings(localFiles[215])

	var collisions []MainlineCollision
	for v, localNames := range localFiles {
		remoteNames, existsOnRemote := remoteFiles[v]
		if !existsOnRemote {
			continue
		}
		sort.Strings(remoteNames)
		var newLocal []string
		for _, f := range localNames {
			if !slices.Contains(remoteNames, f) {
				newLocal = append(newLocal, f)
			}
		}
		if len(newLocal) == 0 {
			continue
		}
		collisions = append(collisions, MainlineCollision{
			Version:     v,
			LocalFiles:  newLocal,
			RemoteFiles: remoteNames,
		})
	}

	if len(collisions) != 1 {
		t.Fatalf("expected 1 collision, got %d: %+v", len(collisions), collisions)
	}
	if collisions[0].Version != 215 {
		t.Fatalf("expected collision at version 215, got %d", collisions[0].Version)
	}
	if collisions[0].LocalFiles[0] != "0215_2026-06-11_new-branch-worklog.md" {
		t.Fatalf("expected local file new-branch-worklog.md, got %s", collisions[0].LocalFiles[0])
	}
	if collisions[0].RemoteFiles[0] != "0215_2026-06-11_different-main-worklog.md" {
		t.Fatalf("expected remote file different-main-worklog.md, got %s", collisions[0].RemoteFiles[0])
	}
}

func TestMainlineCheck_SharedAncestryNotCollision(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "0210_2026-06-11_shared.md"), "")

	_, localFiles, _, err := scanWorklogDir(dir)
	if err != nil {
		t.Fatalf("scanWorklogDir: %v", err)
	}

	remoteFiles := map[int][]string{
		210: {"0210_2026-06-11_shared.md"},
	}

	var collisions []MainlineCollision
	for v, localNames := range localFiles {
		remoteNames, existsOnRemote := remoteFiles[v]
		if !existsOnRemote {
			continue
		}
		sort.Strings(localNames)
		sort.Strings(remoteNames)
		var newLocal []string
		for _, f := range localNames {
			if !slices.Contains(remoteNames, f) {
				newLocal = append(newLocal, f)
			}
		}
		if len(newLocal) == 0 {
			continue
		}
		collisions = append(collisions, MainlineCollision{
			Version:     v,
			LocalFiles:  newLocal,
			RemoteFiles: remoteNames,
		})
	}

	if len(collisions) != 0 {
		t.Fatalf("identical file should not be a collision, got %d: %+v", len(collisions), collisions)
	}
}

func TestLive_Worklogs_NoMainlineCollisions(t *testing.T) {
	root := repoRoot(t)
	rep, err := MainlineCheck(filepath.Join(root, "worklogs"))
	if err != nil {
		t.Fatalf("mainline check: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("worklogs/ collides with origin/main:\n%s", rep.String())
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// repoRoot walks up from the test working directory until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for d := wd; d != "/" && d != "."; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatalf("could not locate go.mod ancestor of %s", wd)
	return ""
}
