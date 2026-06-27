// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package types

import (
	"regexp"

	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

// slugPattern is the canonical URL-safe slug shape: letters (case-insensitive),
// digits, and single hyphens between segments. Leading, trailing, and
// consecutive hyphens are rejected.
//
// This is the format the frontend's slugify() produces from a human-readable
// name (e.g. "My Org" -> "my-org") and matches the de facto convention used
// by GitHub, Slack, etc. Uppercase is accepted at the validation layer and
// lowercased by the handler before persistence (see orgs.go) so that case-
// insensitive uniqueness still works against the DB index.
var slugPattern = regexp.MustCompile(`^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`)

// validateSlug is the "slug" custom validator registered on Gin's binding
// engine in init(). When applied to a string field via `binding:"slug"`, the
// field must match slugPattern. Empty strings pass — combine with `required`
// to require the field.
func validateSlug(fl validator.FieldLevel) bool {
	s := fl.Field().String()
	if s == "" {
		return true
	}
	return slugPattern.MatchString(s)
}

func init() {
	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		// Best-effort registration: a duplicate registration would only
		// happen on re-init, which Go does not do. Ignore the returned
		// error to mirror the convention used in
		// api/internal/middleware/validation.go.
		_ = v.RegisterValidation("slug", validateSlug)
	}
}
