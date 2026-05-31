package repolint

import (
	"os"
	"path/filepath"
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
// helpers
// ---------------------------------------------------------------------------

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
