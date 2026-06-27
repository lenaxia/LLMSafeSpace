// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { SDK_KINDS, SLUG_REGEX, slugFromName } from "./providerCredentialTypes";

// These pure-function tests pin the shared identity helpers that all three
// credential tabs (admin, user, org) depend on. The Go and SQL sides have
// their own property tests (pkg/secrets/credential_identity_test.go) that
// guard against drift; this file is the frontend half.

describe("SLUG_REGEX", () => {
  it("accepts valid slugs", () => {
    const valid = [
      "a",
      "openai",
      "thekaocloud",
      "litellm-prod",
      "litellm-prod-us-west",
      "a1",
      "1a",
      "x" + "a".repeat(62) + "y", // 64 chars (max allowed)
    ];
    for (const s of valid) {
      expect(SLUG_REGEX.test(s)).toBe(true);
    }
  });

  it("rejects invalid slugs — same shapes as the DB CHECK rejects", () => {
    const invalid = [
      "",
      "has space",
      "UPPER",
      "-leading",
      "trailing-",
      "has/slash",
      "has_underscore", // hyphens only, no underscores
      "x" + "a".repeat(64), // 65 chars (too long)
      "--",
      "a/b",
      "a.b",
    ];
    for (const s of invalid) {
      expect(SLUG_REGEX.test(s)).toBe(false);
    }
  });
});

describe("SDK_KINDS", () => {
  it("contains the 15 canonical SDK kinds", () => {
    // Pinned shape — adding a kind requires updating SDK_KINDS, the Go
    // ValidKinds slice in pkg/secrets/credential_identity.go, and the DB
    // CHECK in api/migrations/000001_initial_schema.up.sql in one commit.
    expect(SDK_KINDS).toEqual([
      "openai",
      "anthropic",
      "google",
      "openai_compatible",
      "bedrock",
      "azure_openai",
      "vertex",
      "cohere",
      "mistral",
      "perplexity",
      "groq",
      "xai",
      "openrouter",
      "together",
      "opencode",
    ]);
  });

  it("does not include the legacy 'custom' value", () => {
    // 'custom' was the pre-Epic-55 SDK kind for OpenAI-compatible endpoints.
    // It is no longer in the enum — openai_compatible replaces it. The form
    // dropdown must NOT offer 'custom'.
    expect((SDK_KINDS as readonly string[]).includes("custom")).toBe(false);
  });
});

describe("slugFromName", () => {
  // The auto-suggest function powers the slug input field as the user types
  // a name. It must produce slugs that pass SLUG_REGEX (or empty for inputs
  // that have no extractable alphanumeric content).

  it("lowercases", () => {
    expect(slugFromName("OpenAI")).toBe("openai");
    expect(slugFromName("Anthropic")).toBe("anthropic");
  });

  it("replaces non-alphanumeric runs with single hyphens", () => {
    expect(slugFromName("foo bar")).toBe("foo-bar");
    expect(slugFromName("foo  bar")).toBe("foo-bar");
    expect(slugFromName("foo!@#bar")).toBe("foo-bar");
    expect(slugFromName("foo/bar")).toBe("foo-bar");
    expect(slugFromName("foo.bar.baz")).toBe("foo-bar-baz");
  });

  it("strips leading and trailing hyphens", () => {
    expect(slugFromName("  foo  ")).toBe("foo");
    expect(slugFromName("-foo-")).toBe("foo");
    expect(slugFromName("---foo---")).toBe("foo");
  });

  it("returns empty for inputs with no alphanumeric content", () => {
    // The required-field check downstream catches this — the form won't
    // submit with an empty slug.
    expect(slugFromName("")).toBe("");
    expect(slugFromName("   ")).toBe("");
    expect(slugFromName("!@#$%^&*()")).toBe("");
    expect(slugFromName("---")).toBe("");
  });

  it("truncates to 64 characters and cleans trailing hyphens after truncation", () => {
    // Concrete edge case: a 70-char name that places a hyphen at position 64
    // would, after a naive .slice(0, 64), leave a trailing hyphen that fails
    // SLUG_REGEX. The .replace(/-+$/g, "") fixup must run AFTER the slice.
    const name = "a".repeat(60) + " is a thing";
    const slug = slugFromName(name);
    expect(slug.length).toBeLessThanOrEqual(64);
    expect(slug.endsWith("-")).toBe(false);
    expect(SLUG_REGEX.test(slug)).toBe(true);
  });

  it("handles mixed alphanumerics and spaces", () => {
    expect(slugFromName("My Mix Cred 99")).toBe("my-mix-cred-99");
    expect(slugFromName("LiteLLM Prod US-West")).toBe("litellm-prod-us-west");
  });

  it("matches the SQL backfill expression in 000001_initial_schema.up.sql", () => {
    // The schema migration's backfill uses
    //   BTRIM(REGEXP_REPLACE(LOWER(name), '[^a-z0-9]+', '-', 'g'), '-')
    // This function must produce identical output so a user creating a
    // credential via the API and a user creating one via the UI end up with
    // the same slug for the same input name.
    const cases: Array<[string, string]> = [
      ["thekaocloud", "thekaocloud"],
      ["My Mix Cred 99", "my-mix-cred-99"],
      ["TheKaoCloud", "thekaocloud"],
      ["", ""],
      ["openai-compat", "openai-compat"],
    ];
    for (const [input, expected] of cases) {
      expect(slugFromName(input)).toBe(expected);
    }
  });

  it("every non-empty output passes SLUG_REGEX", () => {
    // Property: whatever slugFromName produces, the server's regex must
    // accept it (or it produces empty, which the required-field check
    // catches earlier).
    const names = [
      "openai",
      "Foo Bar",
      "ALL CAPS",
      "with-existing-hyphens",
      "ends-with-hyphen ",
      " starts-with-hyphen",
      "a",
      "lots of  spaces  between  words",
      "spëçíàl chàrs",
    ];
    for (const n of names) {
      const slug = slugFromName(n);
      if (slug !== "") {
        expect(SLUG_REGEX.test(slug)).toBe(true);
      }
    }
  });
});
