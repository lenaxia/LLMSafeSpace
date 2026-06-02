// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

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

	provs := parsed["providers"].(map[string]interface{})
	anth := provs["anthropic"].(map[string]interface{})
	opts := anth["options"].(map[string]interface{})
	aisdk := opts["aisdk"].(map[string]interface{})
	prov := aisdk["provider"].(map[string]interface{})
	require.Equal(t, "sk-ant-123", prov["apiKey"])

	// No endpoint when BaseURL is empty
	require.Nil(t, anth["endpoint"])
	// No models when Models is empty
	require.Nil(t, anth["models"])
	// No model when Default is empty
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

	provs := parsed["providers"].(map[string]interface{})
	anth := provs["anthropic"].(map[string]interface{})

	// Endpoint
	ep := anth["endpoint"].(map[string]interface{})
	require.Equal(t, "anthropic/messages", ep["type"])
	require.Equal(t, "https://custom.anthropic.com/v1", ep["url"])

	// Models
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

	// First provider's Default wins
	require.Equal(t, "anthropic/claude-sonnet-4-5", parsed["model"])
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

	// model field should be absent
	_, hasModel := parsed["model"]
	require.False(t, hasModel)
}

func TestFormatOpenCodeConfig_EndpointType_OpenAI(t *testing.T) {
	providers := []secrets.LLMProviderData{
		{Provider: "openai", APIKey: "sk-oai", BaseURL: "https://api.openai.com/v1"},
	}

	out, err := FormatOpenCodeConfig(providers)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &parsed))

	provs := parsed["providers"].(map[string]interface{})
	oai := provs["openai"].(map[string]interface{})
	ep := oai["endpoint"].(map[string]interface{})
	require.Equal(t, "openai/responses", ep["type"])
	require.Equal(t, "https://api.openai.com/v1", ep["url"])
}

func TestFormatOpenCodeConfig_EndpointType_UnknownProvider(t *testing.T) {
	providers := []secrets.LLMProviderData{
		{Provider: "deepseek", APIKey: "sk-ds", BaseURL: "https://api.deepseek.com/v1"},
	}

	out, err := FormatOpenCodeConfig(providers)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &parsed))

	provs := parsed["providers"].(map[string]interface{})
	ds := provs["deepseek"].(map[string]interface{})
	ep := ds["endpoint"].(map[string]interface{})
	require.Equal(t, "aisdk", ep["type"])
	require.Equal(t, "@ai-sdk/openai-compatible", ep["package"])
	require.Equal(t, "https://api.deepseek.com/v1", ep["url"])
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

	provs := parsed["providers"].(map[string]interface{})
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
