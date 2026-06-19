// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// resolveRuntimeImage maps workspace.spec.runtime to a container image via
// RuntimeEnvironment CRD lookup.
//
// Lookup strategy:
//  1. Contains '/' → use as-is (explicit image reference)
//  2. Exact name match on RuntimeEnvironment
//  3. ':' replaced by '-' name match (e.g. "python:3.11" → "python-3.11")
//  4. language:version match across all RuntimeEnvironments
func resolveRuntimeImage(
	ctx context.Context,
	c client.Reader,
	runtime string,
) (image string, matchedName string, err error) {
	if runtime == "" {
		return "", "", fmt.Errorf("workspace.spec.runtime is empty")
	}

	if strings.Contains(runtime, "/") {
		return runtime, "", nil
	}

	env := &v1.RuntimeEnvironment{}
	if err := c.Get(ctx, types.NamespacedName{Name: runtime}, env); err == nil {
		if env.Spec.Image == "" {
			return "", env.Name, fmt.Errorf("RuntimeEnvironment %q has empty spec.image", env.Name)
		}
		return env.Spec.Image, env.Name, nil
	} else if !errors.IsNotFound(err) {
		return "", "", fmt.Errorf("looking up RuntimeEnvironment %q: %w", runtime, err)
	}

	if strings.Contains(runtime, ":") {
		alt := strings.ReplaceAll(runtime, ":", "-")
		env := &v1.RuntimeEnvironment{}
		if err := c.Get(ctx, types.NamespacedName{Name: alt}, env); err == nil {
			if env.Spec.Image == "" {
				return "", env.Name, fmt.Errorf("RuntimeEnvironment %q has empty spec.image", env.Name)
			}
			return env.Spec.Image, env.Name, nil
		} else if !errors.IsNotFound(err) {
			return "", "", fmt.Errorf("looking up RuntimeEnvironment %q: %w", alt, err)
		}
	}

	if idx := strings.Index(runtime, ":"); idx > 0 {
		lang := runtime[:idx]
		ver := runtime[idx+1:]
		list := &v1.RuntimeEnvironmentList{}
		if err := c.List(ctx, list); err != nil {
			return "", "", fmt.Errorf("listing RuntimeEnvironments: %w", err)
		}
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
		"no RuntimeEnvironment found matching workspace.spec.runtime=%q", runtime)
}
