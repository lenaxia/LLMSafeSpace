// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"
)

func TestValidate_ValidSpec(t *testing.T) {
	spec := []byte(`
openapi: "3.0.3"
info:
  title: LLMSafeSpaces API
  version: "1.0.0"
paths:
  /health:
    get:
      summary: Health check
      responses:
        "200":
          description: OK
components:
  schemas:
    Error:
      type: object
      properties:
        error:
          type: string
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
`)
	errors := validate(spec)
	if len(errors) > 0 {
		t.Errorf("expected no errors, got: %v", errors)
	}
}

func TestValidate_MissingOpenAPIVersion(t *testing.T) {
	spec := []byte(`
info:
  title: Test
  version: "1.0.0"
paths:
  /x:
    get:
      responses:
        "200":
          description: OK
components:
  schemas:
    X:
      type: object
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
`)
	errors := validate(spec)
	if len(errors) == 0 {
		t.Error("expected error for missing openapi version")
	}
}

func TestValidate_NoPaths(t *testing.T) {
	spec := []byte(`
openapi: "3.0.3"
info:
  title: Test
  version: "1.0.0"
paths: {}
components:
  schemas:
    X:
      type: object
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
`)
	errors := validate(spec)
	found := false
	for _, e := range errors {
		if e == "no paths defined" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'no paths defined' error, got: %v", errors)
	}
}

func TestValidate_UnresolvedRef(t *testing.T) {
	spec := []byte(`
openapi: "3.0.3"
info:
  title: Test
  version: "1.0.0"
paths:
  /x:
    get:
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/DoesNotExist"
components:
  schemas:
    X:
      type: object
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
`)
	errors := validate(spec)
	found := false
	for _, e := range errors {
		if e == "unresolved $ref: #/components/schemas/DoesNotExist" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unresolved ref error, got: %v", errors)
	}
}

func TestValidate_InvalidYAML(t *testing.T) {
	spec := []byte(`{{{invalid yaml`)
	errors := validate(spec)
	if len(errors) == 0 {
		t.Error("expected YAML parse error")
	}
}

func TestValidate_MissingSecuritySchemes(t *testing.T) {
	spec := []byte(`
openapi: "3.0.3"
info:
  title: Test
  version: "1.0.0"
paths:
  /x:
    get:
      responses:
        "200":
          description: OK
components:
  schemas:
    X:
      type: object
`)
	errors := validate(spec)
	found := false
	for _, e := range errors {
		if e == "no securitySchemes defined in components" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing securitySchemes error, got: %v", errors)
	}
}
