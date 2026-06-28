// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// bindingErrorResponse builds the response payload for a c.ShouldBindJSON
// failure. It produces field-level details when the underlying error is a
// validator.ValidationErrors (covering struct-tag validation failures like
// missing fields, bad email, bad slug, etc.), and falls back to a generic
// body-level error for malformed JSON or other binding failures.
//
// The returned shape is:
//
//	{
//	  "error":   "validation failed" | "invalid request body",
//	  "details": { "<jsonFieldName>": "<message>", ... }   // optional
//	}
//
// Keys in details use the JSON tag name on the struct (e.g. "ownerEmail") so
// the frontend can highlight the right form field. The function never returns
// nil — callers can pass the result straight to c.JSON.
func bindingErrorResponse(err error, model any) map[string]any {
	if err == nil {
		return map[string]any{"error": "invalid request body"}
	}

	// JSON syntax/type errors come back as *json.SyntaxError or
	// *json.UnmarshalTypeError. These are body-level — there is no field
	// to attribute them to in any useful way.
	var syntaxErr *json.SyntaxError
	var unmarshalTypeErr *json.UnmarshalTypeError
	if errors.As(err, &syntaxErr) || errors.As(err, &unmarshalTypeErr) {
		return map[string]any{"error": "invalid request body"}
	}

	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return map[string]any{"error": "invalid request body"}
	}

	modelType := reflect.TypeOf(model)
	if modelType != nil && modelType.Kind() == reflect.Ptr {
		modelType = modelType.Elem()
	}

	details := make(map[string]any, len(verrs))
	for _, ve := range verrs {
		key := jsonFieldName(modelType, ve.StructField())
		details[key] = bindingErrorMessage(ve)
	}

	return map[string]any{
		"error":   "validation failed",
		"details": details,
	}
}

// jsonFieldName returns the JSON tag name for a struct field, falling back to
// the lowercased field name when no tag is set. When the model type is not a
// struct (or nil), the field name is returned unchanged.
func jsonFieldName(modelType reflect.Type, fieldName string) string {
	if modelType == nil || modelType.Kind() != reflect.Struct {
		return fieldName
	}
	field, ok := modelType.FieldByName(fieldName)
	if !ok {
		return fieldName
	}
	tag := field.Tag.Get("json")
	if tag == "" || tag == "-" {
		return fieldName
	}
	if comma := strings.Index(tag, ","); comma >= 0 {
		tag = tag[:comma]
	}
	if tag == "" {
		return fieldName
	}
	return tag
}

// bindingErrorMessage returns a short human-readable message for a single
// validator.FieldError. The format mirrors api/internal/middleware/validation.go's
// getValidationErrorMessage() so the two paths stay consistent.
//
// DUPLICATION NOTE: This switch is intentionally duplicated with
// getValidationErrorMessage in the middleware package. The two paths emit
// different envelope shapes (this one: {"error": <str>, "details": {...}};
// middleware: {"error": {"code", "message", "details"}}) so a shared
// formatter would require new abstractions. They MUST be kept in sync: if
// you add or change a case here, update the middleware version too.
// See worklog 0557 follow-up for the centralized-validation story that will
// collapse this duplication.
func bindingErrorMessage(ve validator.FieldError) string {
	switch ve.Tag() {
	case "required":
		return "This field is required"
	case "email":
		return "Invalid email address"
	case "min":
		if ve.Type().Kind() == reflect.String {
			return fmt.Sprintf("Must be at least %s characters long", ve.Param())
		}
		return fmt.Sprintf("Must be greater than or equal to %s", ve.Param())
	case "max":
		if ve.Type().Kind() == reflect.String {
			return fmt.Sprintf("Must be at most %s characters long", ve.Param())
		}
		return fmt.Sprintf("Must be less than or equal to %s", ve.Param())
	case "len":
		return fmt.Sprintf("Must be exactly %s characters long", ve.Param())
	case "oneof":
		return fmt.Sprintf("Must be one of: %s", ve.Param())
	case "alphanum":
		return "Must contain only alphanumeric characters"
	case "slug":
		return "Must be letters, digits, and single hyphens between segments (e.g. \"my-org\")"
	case "uuid":
		return "Must be a valid UUID"
	case "url":
		return "Must be a valid URL"
	default:
		return "Invalid value"
	}
}
