package utilities

import (
)

// MaskSensitiveFieldsWithList masks sensitive fields in a map based on a provided list of field names
func MaskSensitiveFieldsWithList(data map[string]interface{}, sensitiveFields []string) {
	for _, key := range sensitiveFields {
		if value, exists := data[key]; exists {
			// Use MaskString for string values
			if strValue, ok := value.(string); ok {
				data[key] = MaskString(strValue)
			} else {
				data[key] = "********"
			}
		}
	}
	
	// Also check nested maps
	for _, v := range data {
		if nestedMap, ok := v.(map[string]interface{}); ok {
			MaskSensitiveFieldsWithList(nestedMap, sensitiveFields)
		}
	}
}

// MaskSensitiveFields masks common sensitive fields in a map
func MaskSensitiveFields(data map[string]interface{}) {
	sensitiveKeys := []string{"password", "api_key", "apikey", "token", "secret"}
	MaskSensitiveFieldsWithList(data, sensitiveKeys)
}

// MaskString masks a string by showing parts of the beginning and end
// while hiding the middle portion with asterisks
func MaskString(s string) string {
	if len(s) <= 8 {
		return "********"
	} else if len(s) <= 12 {
		return s[:2] + "..." + s[len(s)-2:]
	} else if len(s) <= 20 {
		return s[:3] + "..." + s[len(s)-3:]
	} else {
		return s[:4] + "..." + s[len(s)-4:]
	}
}
      
