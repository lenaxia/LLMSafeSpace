// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package repolint contains lint checks that operate on the repository
// layout itself rather than on Go source code: migration version
// numbering, worklog numbering, and sync between canonical and
// chart-bundled copies of files.
//
// These checks exist because real production incidents have come from
// repo-layout drift (worklog 0097 — two agents both numbered a
// migration "000009", one was silently skipped on cluster, schema
// ended up missing required columns).
//
// The package is consumed by both `cmd/repolint` (the CLI used in
// pre-commit hooks and CI) and `*_test.go` files that assert today's
// repository is in good shape.
package repolint

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Patterns
// ---------------------------------------------------------------------------

// MigrationPattern matches `NNNNNN_<name>.up.sql` and `.down.sql` where
// NNNNNN is six digits. Captures: 1=version, 2=name, 3=direction.
//
// The 6-digit prefix is what golang-migrate uses; matches today's
// `api/migrations/000001_initial_schema.up.sql` style.
var MigrationPattern = regexp.MustCompile(`^(\d{6})_([a-z0-9_]+)\.(up|down)\.sql$`)

// WorklogPattern matches `NNNN_YYYY-MM-DD_<slug>.md`. Captures:
// 1=version, 2=date, 3=slug.
var WorklogPattern = regexp.MustCompile(`^(\d{4})_(\d{4}-\d{2}-\d{2})_([a-z0-9._-]+)\.md$`)

// ---------------------------------------------------------------------------
// SequenceCheck — duplicate / gap / unpaired detection in a versioned dir
// ---------------------------------------------------------------------------

// SequenceConfig configures a single SequenceCheck run.
type SequenceConfig struct {
	// Dir is the directory to scan.
	Dir string
	// Pattern matches versioned filenames. Capture group 1 MUST be the
	// numeric version. If RequirePaired is true, capture group 3 MUST
	// be "up" or "down".
	Pattern *regexp.Regexp
	// RequirePaired, when true, asserts every (version, name) tuple
	// has both an up and a down file. Use false for single-file
	// schemes like worklogs.
	RequirePaired bool
	// GrandfatherBelow, when > 0, exempts existing collisions and
	// gaps at versions strictly less than this value from failing the
	// check. New entries at or above this threshold are still subject
	// to all rules. Use this when historical duplicates exist and
	// rewriting them is impractical (e.g. cross-references in 20+
	// files); the goal is to prevent NEW drift, not relitigate old.
	GrandfatherBelow int
}

// Duplicate is two or more files claiming the same version number.
type Duplicate struct {
	Version int
	Files   []string
}

// SequenceReport is the result of a SequenceCheck run.
type SequenceReport struct {
	// MaxVersion is the highest version found. Zero if dir is empty.
	MaxVersion int
	// SeenVersions are all unique version numbers, sorted.
	SeenVersions []int
	// MissingVersions lists numbers in [1, MaxVersion] that no file
	// covers. (A run with MaxVersion=0 has no missing versions.)
	MissingVersions []int
	// Duplicates lists every version that has more than one
	// (name) entry. Note: a paired up+down counts as ONE entry.
	Duplicates []Duplicate
	// UnpairedFiles lists filenames that lack their matching up/down
	// counterpart (only populated when RequirePaired=true).
	UnpairedFiles []string
}

// OK reports whether the dir is in a healthy state.
func (r SequenceReport) OK() bool {
	return len(r.Duplicates) == 0 && len(r.MissingVersions) == 0 && len(r.UnpairedFiles) == 0
}

// String returns a human-readable failure description, or "(ok)".
func (r SequenceReport) String() string {
	if r.OK() {
		return "(ok)"
	}
	var b strings.Builder
	if len(r.Duplicates) > 0 {
		fmt.Fprintf(&b, "  %d duplicate version(s):\n", len(r.Duplicates))
		for _, d := range r.Duplicates {
			fmt.Fprintf(&b, "    version %d shared by:\n", d.Version)
			for _, f := range d.Files {
				fmt.Fprintf(&b, "      - %s\n", f)
			}
		}
	}
	if len(r.MissingVersions) > 0 {
		fmt.Fprintf(&b, "  gap(s) in sequence — missing version(s): %v (max seen: %d)\n",
			r.MissingVersions, r.MaxVersion)
	}
	if len(r.UnpairedFiles) > 0 {
		fmt.Fprintf(&b, "  %d file(s) lack matching up/down counterpart:\n", len(r.UnpairedFiles))
		for _, f := range r.UnpairedFiles {
			fmt.Fprintf(&b, "    - %s\n", f)
		}
	}
	return b.String()
}

// SequenceCheck scans cfg.Dir for files matching cfg.Pattern and
// reports duplicates, gaps, and (if RequirePaired) unpaired files.
func SequenceCheck(cfg SequenceConfig) (SequenceReport, error) {
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		return SequenceReport{}, fmt.Errorf("read dir %s: %w", cfg.Dir, err)
	}

	// Index by version → set of (name, directions-seen).
	type entryRecord struct {
		name       string // base name (capture group 2 in MigrationPattern; entire match in WorklogPattern)
		filename   string // full filename
		directions map[string]bool
	}
	byVersion := map[int][]*entryRecord{}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := cfg.Pattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		version, err := strconv.Atoi(m[1])
		if err != nil {
			return SequenceReport{}, fmt.Errorf("parse version from %q: %w", e.Name(), err)
		}

		var name, direction string
		if cfg.RequirePaired {
			if len(m) < 4 {
				return SequenceReport{}, fmt.Errorf("RequirePaired=true but pattern has fewer than 3 capture groups (need version, name, direction)")
			}
			name = m[2]
			direction = m[3]
		} else {
			// Use full match minus version as the "name" key — for
			// worklogs this means "0097_2026-05-30_something.md"
			// gets keyed by "2026-05-30_something.md", which is
			// correct for uniqueness purposes.
			name = e.Name()
		}

		// Find or insert an entryRecord for (version, name).
		var rec *entryRecord
		for _, existing := range byVersion[version] {
			if existing.name == name {
				rec = existing
				break
			}
		}
		if rec == nil {
			rec = &entryRecord{
				name:       name,
				filename:   e.Name(),
				directions: map[string]bool{},
			}
			byVersion[version] = append(byVersion[version], rec)
		}
		if direction != "" {
			rec.directions[direction] = true
		}
	}

	// Build the report.
	rep := SequenceReport{}
	for v, recs := range byVersion {
		rep.SeenVersions = append(rep.SeenVersions, v)
		if v > rep.MaxVersion {
			rep.MaxVersion = v
		}
		// Skip duplicate-detection for grandfathered versions; they
		// are historical and cross-referenced widely. The check
		// continues to enforce uniqueness at >= GrandfatherBelow.
		if cfg.GrandfatherBelow > 0 && v < cfg.GrandfatherBelow {
			continue
		}
		if len(recs) > 1 {
			files := make([]string, 0, len(recs)*2)
			for _, r := range recs {
				if cfg.RequirePaired {
					if r.directions["up"] {
						files = append(files, fmt.Sprintf("%06d_%s.up.sql", v, r.name))
					}
					if r.directions["down"] {
						files = append(files, fmt.Sprintf("%06d_%s.down.sql", v, r.name))
					}
				} else {
					files = append(files, r.filename)
				}
			}
			sort.Strings(files)
			rep.Duplicates = append(rep.Duplicates, Duplicate{
				Version: v,
				Files:   files,
			})
		}
		if cfg.RequirePaired {
			for _, r := range recs {
				if !r.directions["up"] {
					rep.UnpairedFiles = append(rep.UnpairedFiles, fmt.Sprintf("%06d_%s.up.sql (down exists, up missing)", v, r.name))
				}
				if !r.directions["down"] {
					rep.UnpairedFiles = append(rep.UnpairedFiles, fmt.Sprintf("%06d_%s.down.sql (up exists, down missing)", v, r.name))
				}
			}
		}
	}
	sort.Ints(rep.SeenVersions)
	sort.Slice(rep.Duplicates, func(i, j int) bool {
		return rep.Duplicates[i].Version < rep.Duplicates[j].Version
	})
	sort.Strings(rep.UnpairedFiles)

	// Detect gaps in [1..MaxVersion]. Versions below GrandfatherBelow
	// are exempt — historical gaps stay.
	if rep.MaxVersion > 0 {
		seen := map[int]bool{}
		for _, v := range rep.SeenVersions {
			seen[v] = true
		}
		for v := 1; v <= rep.MaxVersion; v++ {
			if !seen[v] {
				if cfg.GrandfatherBelow > 0 && v < cfg.GrandfatherBelow {
					continue
				}
				rep.MissingVersions = append(rep.MissingVersions, v)
			}
		}
	}

	return rep, nil
}

// ---------------------------------------------------------------------------
// DriftCheck — compare two directories that should hold identical files
// ---------------------------------------------------------------------------

// DriftConfig configures a DriftCheck run.
type DriftConfig struct {
	// CanonicalDir is the source-of-truth directory.
	CanonicalDir string
	// MirrorDir is the secondary copy that must mirror CanonicalDir.
	MirrorDir string
	// Glob restricts the comparison to files matching this pattern
	// (filepath.Match syntax; relative to dir entries' base names).
	// Files not matching are ignored entirely — including READMEs and
	// other side files that are allowed to differ.
	Glob string
}

// DriftReport is the result of a DriftCheck run.
type DriftReport struct {
	// MissingInMirror are files present in CanonicalDir but absent
	// (or ignored by Glob) in MirrorDir.
	MissingInMirror []string
	// ExtraInMirror are files present in MirrorDir but absent in
	// CanonicalDir.
	ExtraInMirror []string
	// ContentDiffers lists files present in both but with different
	// SHA-256 content hashes.
	ContentDiffers []string
}

// OK reports whether the mirror is byte-identical to the canonical.
func (r DriftReport) OK() bool {
	return len(r.MissingInMirror) == 0 && len(r.ExtraInMirror) == 0 && len(r.ContentDiffers) == 0
}

// String returns a human-readable failure description, or "(ok)".
func (r DriftReport) String() string {
	if r.OK() {
		return "(ok)"
	}
	var b strings.Builder
	if len(r.MissingInMirror) > 0 {
		fmt.Fprintf(&b, "  %d file(s) missing from mirror:\n", len(r.MissingInMirror))
		for _, f := range r.MissingInMirror {
			fmt.Fprintf(&b, "    - %s\n", f)
		}
	}
	if len(r.ExtraInMirror) > 0 {
		fmt.Fprintf(&b, "  %d file(s) present in mirror but not canonical:\n", len(r.ExtraInMirror))
		for _, f := range r.ExtraInMirror {
			fmt.Fprintf(&b, "    - %s\n", f)
		}
	}
	if len(r.ContentDiffers) > 0 {
		fmt.Fprintf(&b, "  %d file(s) have different content:\n", len(r.ContentDiffers))
		for _, f := range r.ContentDiffers {
			fmt.Fprintf(&b, "    - %s\n", f)
		}
	}
	return b.String()
}

// DriftCheck verifies cfg.MirrorDir holds the same files (matching
// cfg.Glob) as cfg.CanonicalDir, byte-for-byte.
func DriftCheck(cfg DriftConfig) (DriftReport, error) {
	canon, err := filteredHashes(cfg.CanonicalDir, cfg.Glob)
	if err != nil {
		return DriftReport{}, fmt.Errorf("hash canonical: %w", err)
	}
	mirror, err := filteredHashes(cfg.MirrorDir, cfg.Glob)
	if err != nil {
		return DriftReport{}, fmt.Errorf("hash mirror: %w", err)
	}

	rep := DriftReport{}
	for name, hCanon := range canon {
		hMirror, ok := mirror[name]
		if !ok {
			rep.MissingInMirror = append(rep.MissingInMirror, name)
			continue
		}
		if hCanon != hMirror {
			rep.ContentDiffers = append(rep.ContentDiffers, name)
		}
	}
	for name := range mirror {
		if _, ok := canon[name]; !ok {
			rep.ExtraInMirror = append(rep.ExtraInMirror, name)
		}
	}
	sort.Strings(rep.MissingInMirror)
	sort.Strings(rep.ExtraInMirror)
	sort.Strings(rep.ContentDiffers)
	return rep, nil
}

func filteredHashes(dir, glob string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		matched, err := filepath.Match(glob, e.Name())
		if err != nil {
			return nil, fmt.Errorf("bad glob %q: %w", glob, err)
		}
		if !matched {
			continue
		}
		h, err := hashFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out[e.Name()] = h
	}
	return out, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// ---------------------------------------------------------------------------
// FixWorklogs — auto-renumber duplicate worklog entries
// ---------------------------------------------------------------------------

// WorklogRename records a single rename performed by FixWorklogs.
type WorklogRename struct {
	From string // original filename (basename only)
	To   string // new filename (basename only)
}

// FixWorklogs resolves duplicate worklog numbers in dir by renaming the
// conflicting file(s) to the next available number above the current
// maximum. It never touches the older file (determined lexically — the
// earlier slug is considered the incumbent) — only the newcomer is moved.
//
// The function iterates until no duplicates remain, handling the
// pathological case where multiple files all collide on the same number.
// It returns the list of renames performed (empty if nothing was needed).
//
// Only files matching WorklogPattern are considered; other files in dir
// are ignored. Versions below the grandfather threshold (97) are never
// touched — historical duplicates stay grandfathered.
//
// After renaming, any occurrence of the old basename inside the file's
// own content is replaced with the new basename, so self-referential
// lines like "worklogs/0140_..._foo.md — This worklog" stay accurate.
func FixWorklogs(dir string) ([]WorklogRename, error) {
	const grandfatherBelow = 97
	var renames []WorklogRename

	for {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return renames, fmt.Errorf("read %s: %w", dir, err)
		}

		byVersion := map[int][]string{}
		maxVer := 0
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			m := WorklogPattern.FindStringSubmatch(e.Name())
			if m == nil {
				continue
			}
			v, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			if v > maxVer {
				maxVer = v
			}
			if v >= grandfatherBelow {
				byVersion[v] = append(byVersion[v], e.Name())
			}
		}

		dupVers := []int{}
		for v, files := range byVersion {
			if len(files) > 1 {
				dupVers = append(dupVers, v)
			}
		}
		if len(dupVers) == 0 {
			break
		}
		sort.Ints(dupVers)

		for _, v := range dupVers {
			files := byVersion[v]
			sort.Strings(files)

			newcomer := files[len(files)-1]
			m := WorklogPattern.FindStringSubmatch(newcomer)
			if m == nil {
				continue
			}
			datePart := m[2]
			slugPart := m[3]

			maxVer++
			newName := fmt.Sprintf("%04d_%s_%s.md", maxVer, datePart, slugPart)

			if err := os.Rename(
				filepath.Join(dir, newcomer),
				filepath.Join(dir, newName),
			); err != nil {
				return renames, fmt.Errorf("rename %s → %s: %w", newcomer, newName, err)
			}

			newPath := filepath.Join(dir, newName)
			if data, err := os.ReadFile(newPath); err == nil {
				updated := strings.ReplaceAll(string(data), newcomer, newName)
				if updated != string(data) {
					_ = os.WriteFile(newPath, []byte(updated), 0o644) //nolint:gosec // markdown docs are not sensitive; 0644 is intentional
				}
			}

			renames = append(renames, WorklogRename{From: newcomer, To: newName})
		}
	}
	return renames, nil
}

// MainlineCollision reports worklog version numbers that exist both locally
// and on the target branch (typically origin/main).
type MainlineCollision struct {
	Version     int
	LocalFiles  []string
	RemoteFiles []string
}

// MainlineReport is the result of a MainlineCheck run.
type MainlineReport struct {
	Collisions []MainlineCollision
	NextNumber int
}

// OK reports whether there are no collisions with mainline.
func (r MainlineReport) OK() bool {
	return len(r.Collisions) == 0
}

// String returns a human-readable failure description, or "(ok)".
func (r MainlineReport) String() string {
	if r.OK() {
		return "(ok)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  %d worklog version(s) collide with %s:\n",
		len(r.Collisions), mainlineRef)
	for _, c := range r.Collisions {
		fmt.Fprintf(&b, "    version %04d:\n", c.Version)
		for _, f := range c.LocalFiles {
			fmt.Fprintf(&b, "      local:  %s\n", f)
		}
		for _, f := range c.RemoteFiles {
			fmt.Fprintf(&b, "      remote: %s\n", f)
		}
	}
	if r.NextNumber > 0 {
		fmt.Fprintf(&b, "  next available worklog number: %04d\n", r.NextNumber)
	}
	return b.String()
}

const mainlineRef = "origin/main"

// MainlineCheck compares local worklog versions against origin/main to
// detect collisions that would cause repolint failures when the branch is
// merged. It also reports the next available worklog number.
//
// The function uses `git ls-tree` to enumerate remote worklog filenames
// without needing a network fetch (assumes origin/main is present in the
// local clone's remote-tracking refs, which is always true after `git clone`
// or `git fetch`).
// MainlineCheck detects worklog version collisions between a branch's NEW
// worklogs (those not yet on origin/main) and worklogs already on origin/main.
// This prevents two branches from choosing the same worklog number and causing
// a repolint failure on merge.
//
// Worklogs that exist identically on both local and remote are NOT flagged —
// they are shared ancestry. Only new worklogs unique to this branch are checked
// for collisions against the remote set.
//
// It also reports the next available worklog number (max of local and remote + 1).
func MainlineCheck(dir string) (MainlineReport, error) {
	_, localFiles, localMax, err := scanWorklogDir(dir)
	if err != nil {
		return MainlineReport{}, err
	}

	_, remoteFiles, remoteMax, err := scanWorklogGit(dir)
	if err != nil {
		return MainlineReport{}, err
	}

	maxVer := localMax
	if remoteMax > maxVer {
		maxVer = remoteMax
	}

	rep := MainlineReport{}
	for v, localNames := range localFiles {
		remoteNames, existsOnRemote := remoteFiles[v]
		if !existsOnRemote {
			continue
		}
		sort.Strings(localNames)
		sort.Strings(remoteNames)

		var newLocal []string
		for _, f := range localNames {
			if !fileInList(f, remoteNames) {
				newLocal = append(newLocal, f)
			}
		}
		if len(newLocal) == 0 {
			continue
		}
		rep.Collisions = append(rep.Collisions, MainlineCollision{
			Version:     v,
			LocalFiles:  newLocal,
			RemoteFiles: remoteNames,
		})
	}
	sort.Slice(rep.Collisions, func(i, j int) bool {
		return rep.Collisions[i].Version < rep.Collisions[j].Version
	})

	if maxVer > 0 {
		rep.NextNumber = maxVer + 1
	}

	return rep, nil
}

func scanWorklogDir(dir string) (map[int]bool, map[int][]string, int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("read dir %s: %w", dir, err)
	}
	versions := map[int]bool{}
	files := map[int][]string{}
	maxVer := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := WorklogPattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		versions[v] = true
		files[v] = append(files[v], e.Name())
		if v > maxVer {
			maxVer = v
		}
	}
	return versions, files, maxVer, nil
}

func fileInList(name string, list []string) bool {
	for _, f := range list {
		if f == name {
			return true
		}
	}
	return false
}

func scanWorklogGit(dir string) (map[int]bool, map[int][]string, int, error) {
	versions := map[int]bool{}
	files := map[int][]string{}
	maxVer := 0

	cmd := exec.Command("git", "ls-tree", "--name-only", mainlineRef, "--", "worklogs/")
	cmd.Dir = filepath.Dir(dir)
	out, err := cmd.Output()
	if err != nil {
		return versions, files, 0, nil
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		base := filepath.Base(line)
		m := WorklogPattern.FindStringSubmatch(base)
		if m == nil {
			continue
		}
		v, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		versions[v] = true
		files[v] = append(files[v], base)
		if v > maxVer {
			maxVer = v
		}
	}
	return versions, files, maxVer, nil
}
