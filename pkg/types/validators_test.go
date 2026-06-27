// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package types

import (
	"testing"

	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

// TestSlugValidator_RegisteredOnGinBinding verifies the init() in
// validators.go successfully registered the slug validator on the binding
// engine. Without this registration the binding tag silently no-ops and
// invalid slugs would slip through to the database.
//
// Gin's defaultValidator calls SetTagName("binding"), so the engine reads
// the `binding:` tag — not the validator package default `validate:`.
func TestSlugValidator_RegisteredOnGinBinding(t *testing.T) {
	v, ok := binding.Validator.Engine().(*validator.Validate)
	if !ok {
		t.Fatal("binding.Validator.Engine() is not *validator.Validate; cannot inspect registration")
	}

	type S struct {
		Slug string `binding:"slug"`
	}
	// A clearly bad slug must produce a validation error if the validator
	// is registered. If it weren't registered, unknown-tag behavior would
	// panic — which is also a test failure but with a less clear message.
	err := v.Struct(S{Slug: "bad slug with spaces"})
	if err == nil {
		t.Fatal("slug validator did not reject input with spaces — likely not registered on Gin's binding engine")
	}
}

// TestSlugPattern_AcceptedShapes exhaustively covers the canonical slug
// shapes we accept. Listed inputs must pass validation.
func TestSlugPattern_AcceptedShapes(t *testing.T) {
	v := validator.New()
	if err := v.RegisterValidation("slug", validateSlug); err != nil {
		t.Fatalf("register slug validator: %v", err)
	}
	type S struct {
		Slug string `validate:"slug"`
	}

	cases := []string{
		"myorg",
		"my-org",
		"my-org-1",
		"a1",
		"123",
		"abc-123-def",
		"MyOrg",  // uppercase accepted; handler lowercases server-side
		"My-Org", // mixed-case with hyphen
	}
	for _, c := range cases {
		c := c
		t.Run(c, func(t *testing.T) {
			if err := v.Struct(S{Slug: c}); err != nil {
				t.Errorf("slug %q should be accepted, got error: %v", c, err)
			}
		})
	}
}

// TestSlugPattern_RejectedShapes covers slugs we reject. Listed inputs must
// fail validation. The empty-string case is intentionally accepted by the
// validator alone (combine with `required` to require the field) — see the
// validator's contract in validators.go.
func TestSlugPattern_RejectedShapes(t *testing.T) {
	v := validator.New()
	if err := v.RegisterValidation("slug", validateSlug); err != nil {
		t.Fatalf("register slug validator: %v", err)
	}
	type S struct {
		Slug string `validate:"slug"`
	}

	cases := []string{
		"my_org",  // underscore
		"my org",  // space
		"my.org",  // dot
		"my/org",  // slash
		"-myorg",  // leading hyphen
		"myorg-",  // trailing hyphen
		"my--org", // consecutive hyphens
		"-",       // single hyphen
		"--",      // hyphens only
		"my-",     // trailing hyphen short
		"-a",      // leading hyphen short
		"my-org!", // punctuation
		"日本語",     // non-ASCII
	}
	for _, c := range cases {
		c := c
		t.Run(c, func(t *testing.T) {
			if err := v.Struct(S{Slug: c}); err == nil {
				t.Errorf("slug %q should be rejected, got nil error", c)
			}
		})
	}
}

// TestSlugValidator_EmptyStringPasses documents the validator's intentional
// behavior: an empty slug is valid in isolation. Required-field semantics
// are layered on by combining tags (`required,slug`).
func TestSlugValidator_EmptyStringPasses(t *testing.T) {
	v := validator.New()
	if err := v.RegisterValidation("slug", validateSlug); err != nil {
		t.Fatalf("register slug validator: %v", err)
	}
	type S struct {
		Slug string `validate:"slug"`
	}
	if err := v.Struct(S{Slug: ""}); err != nil {
		t.Errorf("empty slug should be accepted by validator alone, got: %v", err)
	}
}
