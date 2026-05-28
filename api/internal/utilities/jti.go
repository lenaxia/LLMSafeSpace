package utilities

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// ExtractJTI extracts the jti (JWT ID) claim from a JWT token without
// full validation (validation is already done by ValidateToken).
// Returns empty string if extraction fails.
func ExtractJTI(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		JTI string `json:"jti"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.JTI
}
