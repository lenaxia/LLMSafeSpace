// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

// format_test.go — TDD-style tests pinned to the exact JSON shape
// opencode 1.15.12 accepts.
//
// **CRITICAL**: the schema produced by FormatOpenCodeConfig is
// EVIDENCE-DRIVEN, not derived from a public spec. The shape was
// established by probing a live opencode pod (worklog 0128); pre-fix
// formatters guessed at `providers` (plural) and an `aisdk` nesting
// that opencode rejected with `ConfigInvalidError`. The valid shape is:
//
//   {
//     "$schema": "https://opencode.ai/config.json",
//     "provider": {                              <-- SINGULAR "provider"
//       "<id>": {
//         "options": {                           <-- direct, no aisdk wrapper
//           "apiKey": "...",
//           "baseURL": "..."                     <-- in options, NOT endpoint.url
//         },
//         "models": { "<id>": { "name": "..." } }
//       }
//     },
//     "model": "<id>/<modelID>"
//   }
//
// Every test in this file pins one assertion against this shape.
// If a future schema change ships and we miss it, these tests will
// fail — re-run the worklog 0128 probe to learn the new shape and
// update accordingly.

import (
	"encoding/json"
	"testing"

	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/stretchr/testify/require"
)

func TestFormatOpenCodeConfig_SingleProvider_Minimal(t *testing.T) {
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant-123"},
	}

	out, err := FormatOpenCodeConfig(providers)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &parsed))

	require.Equal(t, "https://opencode.ai/config.json", parsed["$schema"])

	// Top-level key is `provider` (singular).
	provs := parsed["provider"].(map[string]interface{})
	anth := provs["anthropic"].(map[string]interface{})

	// Options carries apiKey directly (no aisdk wrapper).
	opts := anth["options"].(map[string]interface{})
	require.Equal(t, "sk-ant-123", opts["apiKey"])
	_, hasBaseURL := opts["baseURL"]
	require.False(t, hasBaseURL, "no baseURL when BaseURL is empty")

	// No endpoint key — opencode infers from provider id.
	_, hasEndpoint := anth["endpoint"]
	require.False(t, hasEndpoint, "endpoint key must NOT be set; opencode rejects it")

	// No models when Models is empty.
	require.Nil(t, anth["models"])
	// No model when Default is empty.
	require.Nil(t, parsed["model"])
}

func TestFormatOpenCodeConfig_SingleProvider_AllFields(t *testing.T) {
	providers := []secrets.LLMProviderData{
		{
			Provider: "anthropic",
			APIKey:   "sk-ant-123",
			BaseURL:  "https://custom.anthropic.com/v1",
			Default:  "anthropic/claude-sonnet-4-5",
			Models: []secrets.LLMModelConfig{
				{ID: "claude-sonnet-4-5", Label: "Claude Sonnet 4.5"},
				{ID: "claude-haiku-4-5"},
			},
		},
	}

	out, err := FormatOpenCodeConfig(providers)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &parsed))

	require.Equal(t, "anthropic/claude-sonnet-4-5", parsed["model"])

	provs := parsed["provider"].(map[string]interface{})
	anth := provs["anthropic"].(map[string]interface{})

	// baseURL goes into options, NOT into a separate endpoint object.
	opts := anth["options"].(map[string]interface{})
	require.Equal(t, "sk-ant-123", opts["apiKey"])
	require.Equal(t, "https://custom.anthropic.com/v1", opts["baseURL"])

	// No endpoint key.
	_, hasEndpoint := anth["endpoint"]
	require.False(t, hasEndpoint)

	// Models.
	models := anth["models"].(map[string]interface{})
	sonnet := models["claude-sonnet-4-5"].(map[string]interface{})
	require.Equal(t, "Claude Sonnet 4.5", sonnet["name"])
	haiku := models["claude-haiku-4-5"].(map[string]interface{})
	_, hasName := haiku["name"]
	require.False(t, hasName, "model without label should omit name")
}

func TestFormatOpenCodeConfig_MultipleProviders_FirstDefaultWins(t *testing.T) {
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant", Default: "anthropic/claude-sonnet-4-5"},
		{Provider: "openai", APIKey: "sk-oai", Default: "openai/gpt-4o"},
	}

	out, err := FormatOpenCodeConfig(providers)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &parsed))

	require.Equal(t, "anthropic/claude-sonnet-4-5", parsed["model"])
	// Both providers present.
	provs := parsed["provider"].(map[string]interface{})
	require.Contains(t, provs, "anthropic")
	require.Contains(t, provs, "openai")
}

func TestFormatOpenCodeConfig_MultipleProviders_SecondHasDefault(t *testing.T) {
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant"},
		{Provider: "openai", APIKey: "sk-oai", Default: "openai/gpt-4o"},
	}

	out, err := FormatOpenCodeConfig(providers)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &parsed))

	require.Equal(t, "openai/gpt-4o", parsed["model"])
}

func TestFormatOpenCodeConfig_MultipleProviders_NoneHasDefault(t *testing.T) {
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant"},
		{Provider: "openai", APIKey: "sk-oai"},
	}

	out, err := FormatOpenCodeConfig(providers)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &parsed))

	_, hasModel := parsed["model"]
	require.False(t, hasModel)
}

// TestFormatOpenCodeConfig_BaseURL_AppliedFromOptions is the regression
// guard for the second part of Bug 3: pre-fix the baseURL went into a
// separate `endpoint.url` object that opencode discarded, so requests
// went to api.openai.com instead of the operator's endpoint. This test
// asserts baseURL lives at provider.<id>.options.baseURL — the only
// place opencode 1.15.12 reads it from.
func TestFormatOpenCodeConfig_BaseURL_LivesInOptions(t *testing.T) {
	providers := []secrets.LLMProviderData{
		{Provider: "openai", APIKey: "sk-oai", BaseURL: "https://litellm.example/v1"},
	}

	out, err := FormatOpenCodeConfig(providers)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &parsed))

	provs := parsed["provider"].(map[string]interface{})
	oai := provs["openai"].(map[string]interface{})

	// MUST be in options.
	opts := oai["options"].(map[string]interface{})
	require.Equal(t, "https://litellm.example/v1", opts["baseURL"])

	// MUST NOT be in a separate endpoint key.
	_, hasEndpoint := oai["endpoint"]
	require.False(t, hasEndpoint, "Bug 3: endpoint key must NEVER appear; baseURL belongs in options")
}

// TestFormatOpenCodeConfig_TopLevelKey_IsProviderSingular is the
// regression guard for the first part of Bug 3: pre-fix the top-level
// key was `providers` (plural) which opencode 1.15.12 rejected with
// ConfigInvalidError, blocking opencode boot.
func TestFormatOpenCodeConfig_TopLevelKey_IsProviderSingular(t *testing.T) {
	providers := []secrets.LLMProviderData{
		{Provider: "openai", APIKey: "sk-oai"},
	}

	out, err := FormatOpenCodeConfig(providers)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &parsed))

	// MUST have `provider` (singular).
	_, hasProvider := parsed["provider"]
	require.True(t, hasProvider, "Bug 3: top-level key MUST be `provider` (singular)")

	// MUST NOT have `providers` (plural).
	_, hasProviders := parsed["providers"]
	require.False(t, hasProviders, "Bug 3: opencode 1.15.12 rejects the plural `providers` key")
}

// TestFormatOpenCodeConfig_Options_NoAisdkWrapper is the regression
// guard for the third part of Bug 3: pre-fix the apiKey lived at
// options.aisdk.provider.apiKey, which opencode rejected. The valid
// shape is options.apiKey directly.
func TestFormatOpenCodeConfig_Options_NoAisdkWrapper(t *testing.T) {
	providers := []secrets.LLMProviderData{
		{Provider: "openai", APIKey: "sk-oai"},
	}

	out, err := FormatOpenCodeConfig(providers)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &parsed))

	provs := parsed["provider"].(map[string]interface{})
	oai := provs["openai"].(map[string]interface{})
	opts := oai["options"].(map[string]interface{})

	// apiKey directly under options.
	require.Equal(t, "sk-oai", opts["apiKey"])

	// MUST NOT have aisdk wrapper.
	_, hasAisdk := opts["aisdk"]
	require.False(t, hasAisdk, "Bug 3: aisdk wrapper must NEVER appear in options")
}

func TestFormatOpenCodeConfig_ModelsWithAndWithoutLabels(t *testing.T) {
	providers := []secrets.LLMProviderData{
		{
			Provider: "openai",
			APIKey:   "sk-oai",
			Models: []secrets.LLMModelConfig{
				{ID: "gpt-4o", Label: "GPT-4o"},
				{ID: "gpt-4o-mini"},
			},
		},
	}

	out, err := FormatOpenCodeConfig(providers)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &parsed))

	provs := parsed["provider"].(map[string]interface{})
	oai := provs["openai"].(map[string]interface{})
	models := oai["models"].(map[string]interface{})

	gpt4o := models["gpt-4o"].(map[string]interface{})
	require.Equal(t, "GPT-4o", gpt4o["name"])

	mini := models["gpt-4o-mini"].(map[string]interface{})
	_, hasName := mini["name"]
	require.False(t, hasName)
}

func TestFormatOpenCodeConfig_Deterministic(t *testing.T) {
	providers := []secrets.LLMProviderData{
		{Provider: "openai", APIKey: "sk-oai"},
		{Provider: "anthropic", APIKey: "sk-ant"},
		{Provider: "google", APIKey: "sk-goog"},
	}

	out1, err := FormatOpenCodeConfig(providers)
	require.NoError(t, err)

	out2, err := FormatOpenCodeConfig(providers)
	require.NoError(t, err)

	require.Equal(t, string(out1), string(out2), "output must be deterministic")
}

func TestFormatOpenCodeConfig_EmptyInput_Error(t *testing.T) {
	_, err := FormatOpenCodeConfig(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no providers")

	_, err = FormatOpenCodeConfig([]secrets.LLMProviderData{})
	require.Error(t, err)
}

func TestFormatOpenCodeConfig_OutputIsValidJSON(t *testing.T) {
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant-with-special-chars-!@#$%"},
	}

	out, err := FormatOpenCodeConfig(providers)
	require.NoError(t, err)
	require.True(t, json.Valid(out), "output must be valid JSON")
}

// TestFormatOpenCodeConfig_ExactSnapshot pins the exact byte output
// for a representative input. This is the most aggressive regression
// guard: any change to the formatter that affects the wire shape will
// fail this test.
//
// The snapshot below was captured against the EXACT config that we
// verified opencode 1.15.12 accepts (worklog 0128 cluster probe). Do
// NOT update this snapshot without re-validating against a live
// opencode that the new shape produces a connected provider in
// `/provider`.
func TestFormatOpenCodeConfig_ExactSnapshot(t *testing.T) {
	providers := []secrets.LLMProviderData{
		{
			Provider: "openai",
			APIKey:   "sk-test-key",
			BaseURL:  "https://litellm.example/v1",
			Default:  "openai/deepseek-v3-chat",
			Models: []secrets.LLMModelConfig{
				{ID: "deepseek-v3-chat", Label: "DeepSeek V3 Chat"},
			},
		},
	}

	out, err := FormatOpenCodeConfig(providers)
	require.NoError(t, err)

	expected := `{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "openai": {
      "options": {
        "apiKey": "sk-test-key",
        "baseURL": "https://litellm.example/v1"
      },
      "models": {
        "deepseek-v3-chat": {
          "name": "DeepSeek V3 Chat"
        }
      }
    }
  },
  "model": "openai/deepseek-v3-chat"
}`

	require.Equal(t, expected, string(out),
		"snapshot mismatch — opencode rejects shapes other than this one. "+
			"Re-validate against a live opencode pod before updating the snapshot.")
}
