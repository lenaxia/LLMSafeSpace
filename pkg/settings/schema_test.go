package settings

import (
	"testing"
)

func TestSchemaVersion(t *testing.T) {
	if SchemaVersion < 1 {
		t.Errorf("SchemaVersion must be >= 1, got %d", SchemaVersion)
	}
}

func TestInstanceSettings_AllHaveTier2(t *testing.T) {
	for _, def := range InstanceSettings() {
		if def.Tier != 2 {
			t.Errorf("instance setting %q has tier %d, expected 2", def.Key, def.Tier)
		}
	}
}

func TestUserSettings_AllHaveTier3(t *testing.T) {
	for _, def := range UserSettings() {
		if def.Tier != 3 {
			t.Errorf("user setting %q has tier %d, expected 3", def.Key, def.Tier)
		}
	}
}

func TestInstanceSettings_UniqueKeys(t *testing.T) {
	seen := make(map[string]bool)
	for _, def := range InstanceSettings() {
		if seen[def.Key] {
			t.Errorf("duplicate instance setting key: %q", def.Key)
		}
		seen[def.Key] = true
	}
}

func TestUserSettings_UniqueKeys(t *testing.T) {
	seen := make(map[string]bool)
	for _, def := range UserSettings() {
		if seen[def.Key] {
			t.Errorf("duplicate user setting key: %q", def.Key)
		}
		seen[def.Key] = true
	}
}

func TestAllSettings_NoKeyOverlap(t *testing.T) {
	seen := make(map[string]bool)
	for _, def := range AllSettings() {
		if seen[def.Key] {
			t.Errorf("key %q appears in both instance and user settings", def.Key)
		}
		seen[def.Key] = true
	}
}

func TestInstanceSettings_DefaultsPassValidation(t *testing.T) {
	for _, def := range InstanceSettings() {
		if err := Validate(def, def.Default); err != nil {
			t.Errorf("instance setting %q default fails validation: %v", def.Key, err)
		}
	}
}

func TestUserSettings_DefaultsPassValidation(t *testing.T) {
	for _, def := range UserSettings() {
		if err := Validate(def, def.Default); err != nil {
			t.Errorf("user setting %q default fails validation: %v", def.Key, err)
		}
	}
}

func TestAllSettings_HaveRequiredFields(t *testing.T) {
	for _, def := range AllSettings() {
		if def.Key == "" {
			t.Error("setting with empty key")
		}
		if def.Type == "" {
			t.Errorf("setting %q has empty type", def.Key)
		}
		if def.Category == "" {
			t.Errorf("setting %q has empty category", def.Key)
		}
		if def.Label == "" {
			t.Errorf("setting %q has empty label", def.Key)
		}
		if def.Description == "" {
			t.Errorf("setting %q has empty description", def.Key)
		}
		if def.Default == nil {
			t.Errorf("setting %q has nil default", def.Key)
		}
	}
}

func TestInstanceSettingIndex(t *testing.T) {
	idx := InstanceSettingIndex()
	if len(idx) != len(InstanceSettings()) {
		t.Errorf("index has %d entries, expected %d", len(idx), len(InstanceSettings()))
	}
	def, ok := idx["auth.registrationEnabled"]
	if !ok {
		t.Fatal("auth.registrationEnabled not in index")
	}
	if def.Type != TypeBool {
		t.Errorf("expected TypeBool, got %v", def.Type)
	}
}

func TestUserSettingIndex(t *testing.T) {
	idx := UserSettingIndex()
	if len(idx) != len(UserSettings()) {
		t.Errorf("index has %d entries, expected %d", len(idx), len(UserSettings()))
	}
	def, ok := idx["theme"]
	if !ok {
		t.Fatal("theme not in index")
	}
	if def.Type != TypeEnum {
		t.Errorf("expected TypeEnum, got %v", def.Type)
	}
}

func TestValidate_Bool_Happy(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeBool, Default: true}
	if err := Validate(def, true); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := Validate(def, false); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Bool_WrongType(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeBool, Default: true}
	if err := Validate(def, "true"); err == nil {
		t.Error("expected error for string value on bool setting")
	}
	if err := Validate(def, 1); err == nil {
		t.Error("expected error for int value on bool setting")
	}
}

func TestValidate_Int_Happy(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeInt, Default: 5, Min: intPtr(1), Max: intPtr(100)}
	if err := Validate(def, 50); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := Validate(def, 1); err != nil {
		t.Errorf("unexpected error for min boundary: %v", err)
	}
	if err := Validate(def, 100); err != nil {
		t.Errorf("unexpected error for max boundary: %v", err)
	}
}

func TestValidate_Int_BelowMin(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeInt, Default: 5, Min: intPtr(1), Max: intPtr(100)}
	if err := Validate(def, 0); err == nil {
		t.Error("expected error for value below min")
	}
}

func TestValidate_Int_AboveMax(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeInt, Default: 5, Min: intPtr(1), Max: intPtr(100)}
	if err := Validate(def, 101); err == nil {
		t.Error("expected error for value above max")
	}
}

func TestValidate_Int_WrongType(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeInt, Default: 5}
	if err := Validate(def, "5"); err == nil {
		t.Error("expected error for string value on int setting")
	}
}

func TestValidate_Int_Float64FromJSON(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeInt, Default: 5, Min: intPtr(1), Max: intPtr(100)}
	// JSON unmarshals numbers as float64
	if err := Validate(def, float64(50)); err != nil {
		t.Errorf("unexpected error for float64(50): %v", err)
	}
	// Non-integer float64 should fail
	if err := Validate(def, 5.5); err == nil {
		t.Error("expected error for non-integer float64")
	}
}

func TestValidate_String_Happy(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeString, Default: "1Gi", Pattern: `^[0-9]+(Gi|Mi)$`}
	if err := Validate(def, "1Gi"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := Validate(def, "512Mi"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_String_PatternMismatch(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeString, Default: "1Gi", Pattern: `^[0-9]+(Gi|Mi)$`}
	if err := Validate(def, "invalid"); err == nil {
		t.Error("expected error for pattern mismatch")
	}
}

func TestValidate_String_NoPattern(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeString, Default: ""}
	if err := Validate(def, "anything goes"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_String_WrongType(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeString, Default: ""}
	if err := Validate(def, 123); err == nil {
		t.Error("expected error for int value on string setting")
	}
}

func TestValidate_Enum_Happy(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeEnum, Default: "a", Enum: []string{"a", "b", "c"}}
	if err := Validate(def, "a"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := Validate(def, "c"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Enum_InvalidValue(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeEnum, Default: "a", Enum: []string{"a", "b", "c"}}
	if err := Validate(def, "d"); err == nil {
		t.Error("expected error for invalid enum value")
	}
}

func TestValidate_Enum_WrongType(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeEnum, Default: "a", Enum: []string{"a", "b"}}
	if err := Validate(def, 1); err == nil {
		t.Error("expected error for int value on enum setting")
	}
}

func TestValidate_Strings_Happy(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeStrings, Default: []string{}}
	if err := Validate(def, []string{"a", "b"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := Validate(def, []string{}); err != nil {
		t.Errorf("unexpected error for empty slice: %v", err)
	}
}

func TestValidate_Strings_AnySliceFromJSON(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeStrings, Default: []string{}}
	// JSON unmarshals to []interface{}
	if err := Validate(def, []any{"a", "b"}); err != nil {
		t.Errorf("unexpected error for []any: %v", err)
	}
}

func TestValidate_Strings_AnySliceWithNonString(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeStrings, Default: []string{}}
	if err := Validate(def, []any{"a", 123}); err == nil {
		t.Error("expected error for non-string element in []any")
	}
}

func TestValidate_Strings_WrongType(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeStrings, Default: []string{}}
	if err := Validate(def, "not a slice"); err == nil {
		t.Error("expected error for string value on strings setting")
	}
}

func TestValidate_UnknownType(t *testing.T) {
	def := SettingDef{Key: "test", Type: "unknown", Default: "x"}
	if err := Validate(def, "x"); err == nil {
		t.Error("expected error for unknown type")
	}
}
