package sandbox

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// resolveRuntimeImage maps a Sandbox's spec.runtime field (e.g.
// "python:3.11", "llmsafespace/runtime-base:dev", "python-3.11") to a
// concrete container image by looking up the corresponding cluster-scoped
// RuntimeEnvironment.
//
// Lookup strategy, in order:
//
//  1. If `spec.runtime` is itself a fully-qualified container image
//     reference (contains '/'), use it as-is. This is an escape hatch for
//     development and lets operators bypass RuntimeEnvironment for ad-hoc
//     testing. A raw image name like "python:3.11" is NOT treated as
//     fully-qualified because it could conflict with the legacy schema.
//  2. RuntimeEnvironment with metadata.name == spec.runtime exactly.
//  3. RuntimeEnvironment with metadata.name == spec.runtime with ':'
//     replaced by '-' (so "python:3.11" finds "python-3.11").
//  4. List all RuntimeEnvironments and find one whose
//     spec.language + ":" + spec.version equals spec.runtime. Picks the
//     first match in alphabetical order by name (deterministic).
//
// Returns the resolved image and the matched RuntimeEnvironment name (for
// status / logging). Returns an error if nothing matches.
//
// A nil RuntimeEnvironment returned with a non-empty image (case 1) means
// the operator opted out of RuntimeEnvironment resolution.
func resolveRuntimeImage(
	ctx context.Context,
	c client.Reader,
	runtime string,
) (image string, matchedName string, err error) {
	if runtime == "" {
		return "", "", fmt.Errorf("sandbox.spec.runtime is empty")
	}

	// Case 1: explicit container image reference.
	// We treat anything containing '/' as fully-qualified (e.g.
	// "ghcr.io/foo/bar:tag", "llmsafespace/runtime-base:dev"). Bare names
	// like "python:3.11" still go through RuntimeEnvironment lookup so
	// users can't accidentally pull untrusted images by typo.
	if strings.Contains(runtime, "/") {
		return runtime, "", nil
	}

	// Case 2: exact name match.
	env := &v1.RuntimeEnvironment{}
	if err := c.Get(ctx, types.NamespacedName{Name: runtime}, env); err == nil {
		if env.Spec.Image == "" {
			return "", env.Name, fmt.Errorf(
				"RuntimeEnvironment %q has empty spec.image", env.Name)
		}
		return env.Spec.Image, env.Name, nil
	} else if !errors.IsNotFound(err) {
		return "", "", fmt.Errorf("looking up RuntimeEnvironment %q: %w", runtime, err)
	}

	// Case 3: ':' → '-' (e.g. "python:3.11" → "python-3.11").
	if strings.Contains(runtime, ":") {
		alt := strings.ReplaceAll(runtime, ":", "-")
		env := &v1.RuntimeEnvironment{}
		if err := c.Get(ctx, types.NamespacedName{Name: alt}, env); err == nil {
			if env.Spec.Image == "" {
				return "", env.Name, fmt.Errorf(
					"RuntimeEnvironment %q has empty spec.image", env.Name)
			}
			return env.Spec.Image, env.Name, nil
		} else if !errors.IsNotFound(err) {
			return "", "", fmt.Errorf("looking up RuntimeEnvironment %q: %w", alt, err)
		}
	}

	// Case 4: language:version match. Parse "python:3.11" into ("python", "3.11").
	if idx := strings.Index(runtime, ":"); idx > 0 {
		lang := runtime[:idx]
		ver := runtime[idx+1:]
		list := &v1.RuntimeEnvironmentList{}
		if err := c.List(ctx, list); err != nil {
			return "", "", fmt.Errorf("listing RuntimeEnvironments: %w", err)
		}
		// Stable order: pick first match alphabetically.
		// (Map iteration order in Go is randomized; sorting list.Items by
		// Name avoids different controllers picking different images.)
		bestName := ""
		bestImage := ""
		for _, e := range list.Items {
			if e.Spec.Language == lang && e.Spec.Version == ver && e.Spec.Image != "" {
				if bestName == "" || e.Name < bestName {
					bestName = e.Name
					bestImage = e.Spec.Image
				}
			}
		}
		if bestName != "" {
			return bestImage, bestName, nil
		}
	}

	return "", "", fmt.Errorf(
		"no RuntimeEnvironment found matching sandbox.spec.runtime=%q "+
			"(tried name, name-with-':'-replaced, language+version match)",
		runtime)
}
