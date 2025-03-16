package utilities

import (
)

// MaskSensitiveFieldsWithList masks sensitive fields in a map based on a provided list of field names
func MaskSensitiveFieldsWithList(data map[string]interface{}, sensitiveFields []string) {
	for _, key := range sensitiveFields {
		if _, exists := data[key]; exists {
			data[key] = "********"
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

// MaskString masks a string by replacing all but the first and last characters with asterisks
func MaskString(s string) string {
	if len(s) <= 8 {
            return "********"
        } else if len(s) <= 12 {
            return s[:1] + "..." + s[len(s)-2:]
        }
        return s[:4] + "..." + s[len(s)-4:]
}
      
