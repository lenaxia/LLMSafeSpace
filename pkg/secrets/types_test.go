package secrets

import (
	"encoding/json"
	"testing"
)

func TestLLMProviderData_MarshalUnmarshal(t *testing.T) {
	src := LLMProviderData{
		Provider:   "anthropic",
		APIKey:     "sk-ant-api03-secret",
		BaseURL:    "https://api.anthropic.com/v1",
		Default:    "anthropic/claude-sonnet-4-5-20250929",
		SmallModel: "anthropic/claude-haiku-4-5-20250929",
		Models: []LLMModelConfig{
			{ID: "claude-sonnet-4-5-20250929", Name: "Claude Sonnet 4.5"},
			{ID: "claude-haiku-4-5-20250929"},
		},
	}

	data, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var dst LLMProviderData
	if err := json.Unmarshal(data, &dst); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if dst.Provider != src.Provider {
		t.Errorf("Provider: got %q, want %q", dst.Provider, src.Provider)
	}
	if dst.APIKey != src.APIKey {
		t.Errorf("APIKey: got %q, want %q", dst.APIKey, src.APIKey)
	}
	if dst.BaseURL != src.BaseURL {
		t.Errorf("BaseURL: got %q, want %q", dst.BaseURL, src.BaseURL)
	}
	if dst.Default != src.Default {
		t.Errorf("Default: got %q, want %q", dst.Default, src.Default)
	}
	if dst.SmallModel != src.SmallModel {
		t.Errorf("SmallModel: got %q, want %q", dst.SmallModel, src.SmallModel)
	}
	if len(dst.Models) != 2 {
		t.Fatalf("Models: got %d, want 2", len(dst.Models))
	}
	if dst.Models[0].ID != "claude-sonnet-4-5-20250929" {
		t.Errorf("Models[0].ID: got %q", dst.Models[0].ID)
	}
	if dst.Models[0].Name != "Claude Sonnet 4.5" {
		t.Errorf("Models[0].Name: got %q", dst.Models[0].Name)
	}
}

func TestLLMProviderData_MarshalUnmarshal_Minimal(t *testing.T) {
	src := LLMProviderData{
		Provider: "openai",
		APIKey:   "sk-openai-key",
	}

	data, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var dst LLMProviderData
	if err := json.Unmarshal(data, &dst); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if dst.Provider != "openai" {
		t.Errorf("Provider: got %q, want openai", dst.Provider)
	}
	if dst.APIKey != "sk-openai-key" {
		t.Errorf("APIKey: got %q", dst.APIKey)
	}
	if dst.BaseURL != "" {
		t.Errorf("BaseURL should be empty for minimal input, got %q", dst.BaseURL)
	}
	if dst.Default != "" {
		t.Errorf("Default should be empty for minimal input, got %q", dst.Default)
	}
	if len(dst.Models) != 0 {
		t.Errorf("Models should be empty for minimal input, got %d", len(dst.Models))
	}
}

func TestLLMProviderData_Unmarshal_InvalidJSON(t *testing.T) {
	var dst LLMProviderData
	err := json.Unmarshal([]byte(`not json`), &dst)
	if err == nil {
		t.Fatal("Expected error for invalid JSON")
	}
}

func TestLLMProviderData_Unmarshal_ExtraFields(t *testing.T) {
	raw := `{"provider":"anthropic","apiKey":"sk-...","unknown_field":"value"}`
	var dst LLMProviderData
	if err := json.Unmarshal([]byte(raw), &dst); err != nil {
		t.Fatalf("Unmarshal with extra fields should succeed: %v", err)
	}
	if dst.Provider != "anthropic" {
		t.Errorf("Provider: got %q", dst.Provider)
	}
}

func TestLLMProviderData_Models_OmitEmpty(t *testing.T) {
	src := LLMProviderData{
		Provider: "anthropic",
		APIKey:   "sk-...",
	}

	data, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal to map failed: %v", err)
	}

	if _, ok := raw["models"]; ok {
		t.Error("models key should be omitted when empty (omitempty)")
	}
}

func TestSecretTypeLLMProvider_Valid(t *testing.T) {
	if !ValidSecretTypes[SecretTypeLLMProvider] {
		t.Error("SecretTypeLLMProvider should be in ValidSecretTypes")
	}
}

func TestSecretTypeLLMProvider_InTypeList(t *testing.T) {
	types := ValidSecretTypesList()
	found := false
	for _, st := range types {
		if st == SecretTypeLLMProvider {
			found = true
			break
		}
	}
	if !found {
		t.Error("SecretTypeLLMProvider should appear in ValidSecretTypesList")
	}
}

func TestLLMProvider_MetadataRequirements(t *testing.T) {
	reqs, ok := MetadataRequirementsBySecretType[SecretTypeLLMProvider]
	if !ok {
		t.Fatal("MetadataRequirementsBySecretType should have entry for llm-provider")
	}
	// llm-provider metadata is optional; only the "provider" key is suggested
	if len(reqs) < 1 {
		t.Error("llm-provider should have at least 'provider' in metadata requirements")
	}
}
