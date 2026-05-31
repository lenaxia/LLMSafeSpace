// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package utilities

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// HashString
// ---------------------------------------------------------------------------

func TestHashString_KnownValue(t *testing.T) {
	// SHA-256 of "hello" — precomputed reference value
	expected := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	assert.Equal(t, expected, HashString("hello"))
}

func TestHashString_EmptyString(t *testing.T) {
	// SHA-256 of "" — precomputed reference value
	expected := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	assert.Equal(t, expected, HashString(""))
}

func TestHashString_Deterministic(t *testing.T) {
	input := "some-api-key-value"
	assert.Equal(t, HashString(input), HashString(input))
}

func TestHashString_DifferentInputsDifferentOutputs(t *testing.T) {
	assert.NotEqual(t, HashString("abc"), HashString("def"))
}

func TestHashString_OutputLength(t *testing.T) {
	// SHA-256 hex is always 64 characters
	assert.Len(t, HashString("anything"), 64)
	assert.Len(t, HashString(""), 64)
}

// ---------------------------------------------------------------------------
// MaskString
// ---------------------------------------------------------------------------

func TestMaskString_ShortValue(t *testing.T) {
	// ≤ 8 chars → all asterisks
	cases := []string{"", "a", "secret", "12345678"}
	for _, s := range cases {
		assert.Equal(t, "********", MaskString(s), "input=%q", s)
	}
}

func TestMaskString_MediumValue_9to12(t *testing.T) {
	// 9–12 chars → first 2 + "..." + last 2
	s := "123456789" // 9 chars
	result := MaskString(s)
	assert.Equal(t, "12...89", result)
}

func TestMaskString_Medium2_13to20(t *testing.T) {
	// 13–20 chars → first 3 + "..." + last 3
	s := "abcdefghijklm" // 13 chars
	result := MaskString(s)
	assert.Equal(t, "abc...klm", result)
}

func TestMaskString_LongValue(t *testing.T) {
	// > 20 chars → first 4 + "..." + last 4
	s := "abcdefghijklmnopqrstu" // 21 chars
	result := MaskString(s)
	assert.Equal(t, "abcd...rstu", result)
}

func TestMaskString_ExactBoundary8(t *testing.T) {
	assert.Equal(t, "********", MaskString("12345678"))
}

func TestMaskString_ExactBoundary12(t *testing.T) {
	// 12 chars → still in the "2+...+2" bucket (≤ 12)
	s := "123456789012"
	result := MaskString(s)
	assert.Equal(t, "12...12", result)
}

func TestMaskString_ExactBoundary20(t *testing.T) {
	// 20 chars → "3+...+3" bucket (≤ 20)
	s := "12345678901234567890"
	result := MaskString(s)
	assert.Equal(t, "123...890", result)
}

// ---------------------------------------------------------------------------
// MaskSensitiveFields
// ---------------------------------------------------------------------------

func TestMaskSensitiveFields_MasksKnownKeys(t *testing.T) {
	data := map[string]interface{}{
		"username": "alice",
		"password": "super-secret-password!",
		"api_key":  "lsp_abcdefghijklmnopqrst",
		"apikey":   "lsp_abcdefghijklmnopqrst",
		"token":    "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9",
		"secret":   "s3cr3t-value",
	}

	MaskSensitiveFields(data)

	assert.Equal(t, "alice", data["username"], "non-sensitive fields must not change")
	for _, key := range []string{"password", "api_key", "apikey", "token", "secret"} {
		val, _ := data[key].(string)
		assert.NotEqual(t, "", val, "key %q should not be empty after masking", key)
		assert.NotContains(t, val, "super", "key %q should be masked", key)
	}
}

func TestMaskSensitiveFields_NonStringValueMasked(t *testing.T) {
	data := map[string]interface{}{
		"password": 12345,
	}
	MaskSensitiveFields(data)
	assert.Equal(t, "********", data["password"])
}

func TestMaskSensitiveFields_EmptyMap(t *testing.T) {
	data := map[string]interface{}{}
	MaskSensitiveFields(data) // must not panic
}

// ---------------------------------------------------------------------------
// MaskSensitiveFieldsWithList
// ---------------------------------------------------------------------------

func TestMaskSensitiveFieldsWithList_CustomFields(t *testing.T) {
	data := map[string]interface{}{
		"credit_card": "4111-1111-1111-1111",
		"cvv":         "123",
		"name":        "Alice",
	}

	MaskSensitiveFieldsWithList(data, []string{"credit_card", "cvv"})

	assert.Equal(t, "Alice", data["name"])
	cc := data["credit_card"].(string)
	assert.NotEqual(t, "4111-1111-1111-1111", cc)
	cvv := data["cvv"].(string)
	assert.Equal(t, "********", cvv) // 3 chars → "********"
}

func TestMaskSensitiveFieldsWithList_NestedMap(t *testing.T) {
	nested := map[string]interface{}{
		"password": "inner-secret-value-here",
	}
	data := map[string]interface{}{
		"user":   nested,
		"public": "visible",
	}

	MaskSensitiveFieldsWithList(data, []string{"password"})

	assert.Equal(t, "visible", data["public"])
	assert.NotEqual(t, "inner-secret-value-here", nested["password"])
}

func TestMaskSensitiveFieldsWithList_MissingKeyIsNoop(t *testing.T) {
	data := map[string]interface{}{
		"name": "Alice",
	}
	MaskSensitiveFieldsWithList(data, []string{"nonexistent_key"})
	assert.Equal(t, "Alice", data["name"])
}
