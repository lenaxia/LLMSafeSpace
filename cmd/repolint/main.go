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
//	repolint                # run all checks against the repo root
//	repolint -repo /path    # run checks against an alternate root
//	repolint -fix-drift     # also: copy api/migrations/ → charts/llmsafespace/migrations/
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

	failures := 0
	failures += runMigrations(root)
	failures += runWorklogs(root)
	failures += runChartDrift(root)

	if failures > 0 {
		fmt.Fprintf(os.Stderr, "\nrepolint: %d check(s) failed\n", failures)
		os.Exit(exitFailures)
	}
	fmt.Println("repolint: all checks passed")
	os.Exit(exitOK)
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
