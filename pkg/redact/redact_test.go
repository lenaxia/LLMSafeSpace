package redact_test

import (
	"os"
	"testing"

	"github.com/lenaxia/llmsafespace/pkg/redact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackageLevelRedact(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "no secrets",
			input: "Hello, world! No secrets here.",
			want:  "Hello, world! No secrets here.",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := redact.Redact(tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.want, result)
		})
	}
}

func TestURLCredentials(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantContains   string
		wantNoContains string
		wantEqual      string
	}{
		{
			name:           "match",
			input:          "Connect to https://user:s3cr3t@example.com/db",
			wantContains:   "://[REDACTED]@",
			wantNoContains: "s3cr3t",
		},
		{
			name:      "no match",
			input:     "Connect to https://example.com/db",
			wantEqual: "Connect to https://example.com/db",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := redact.Redact(tc.input)
			require.NoError(t, err)
			if tc.wantEqual != "" {
				assert.Equal(t, tc.wantEqual, result)
			}
			if tc.wantContains != "" {
				assert.Contains(t, result, tc.wantContains)
			}
			if tc.wantNoContains != "" {
				assert.NotContains(t, result, tc.wantNoContains)
			}
		})
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantContains   string
		wantNoContains string
		wantEqual      string
	}{
		{
			name:           "match",
			input:          "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.token",
			wantContains:   "[REDACTED]",
			wantNoContains: "eyJhbGciOiJIUzI1NiJ9",
		},
		{
			name:           "case insensitive",
			input:          "authorization: bearer mytoken123",
			wantContains:   "[REDACTED]",
			wantNoContains: "mytoken123",
		},
		{
			name:      "no match",
			input:     "Use basic authentication instead",
			wantEqual: "Use basic authentication instead",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := redact.Redact(tc.input)
			require.NoError(t, err)
			if tc.wantEqual != "" {
				assert.Equal(t, tc.wantEqual, result)
			}
			if tc.wantContains != "" {
				assert.Contains(t, result, tc.wantContains)
			}
			if tc.wantNoContains != "" {
				assert.NotContains(t, result, tc.wantNoContains)
			}
		})
	}
}

func TestGitHubToken(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantContains   string
		wantNoContains string
		wantEqual      string
	}{
		{
			name:           "match",
			input:          "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890",
			wantContains:   "[REDACTED-GH-TOKEN]",
			wantNoContains: "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890",
		},
		{
			name:      "no match short token",
			input:     "ghp_short",
			wantEqual: "ghp_short",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := redact.Redact(tc.input)
			require.NoError(t, err)
			if tc.wantEqual != "" {
				assert.Equal(t, tc.wantEqual, result)
			}
			if tc.wantContains != "" {
				assert.Contains(t, result, tc.wantContains)
			}
			if tc.wantNoContains != "" {
				assert.NotContains(t, result, tc.wantNoContains)
			}
		})
	}
}

func TestJSONPassword(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantContains   string
		wantNoContains string
		wantEqual      string
	}{
		{
			name:           "match",
			input:          `{"username":"admin","password":"supersecretpassword"}`,
			wantContains:   `"password":"[REDACTED]"`,
			wantNoContains: "supersecretpassword",
		},
		{
			name:      "no match",
			input:     `{"username":"admin","email":"admin@example.com"}`,
			wantEqual: `{"username":"admin","email":"admin@example.com"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := redact.Redact(tc.input)
			require.NoError(t, err)
			if tc.wantEqual != "" {
				assert.Equal(t, tc.wantEqual, result)
			}
			if tc.wantContains != "" {
				assert.Contains(t, result, tc.wantContains)
			}
			if tc.wantNoContains != "" {
				assert.NotContains(t, result, tc.wantNoContains)
			}
		})
	}
}

func TestPasswordEquals(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantContains   string
		wantNoContains string
		wantEqual      string
	}{
		{
			name:           "match",
			input:          "password=mysecretvalue",
			wantContains:   "password=[REDACTED]",
			wantNoContains: "mysecretvalue",
		},
		{
			name:      "no match",
			input:     "The password field is required",
			wantEqual: "The password field is required",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := redact.Redact(tc.input)
			require.NoError(t, err)
			if tc.wantEqual != "" {
				assert.Equal(t, tc.wantEqual, result)
			}
			if tc.wantContains != "" {
				assert.Contains(t, result, tc.wantContains)
			}
			if tc.wantNoContains != "" {
				assert.NotContains(t, result, tc.wantNoContains)
			}
		})
	}
}

func TestTokenEquals(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantContains   string
		wantNoContains string
		wantEqual      string
	}{
		{
			name:           "match",
			input:          "token=abc123secrettoken",
			wantContains:   "token=[REDACTED]",
			wantNoContains: "abc123secrettoken",
		},
		{
			name:      "no match",
			input:     "The token is required for access",
			wantEqual: "The token is required for access",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := redact.Redact(tc.input)
			require.NoError(t, err)
			if tc.wantEqual != "" {
				assert.Equal(t, tc.wantEqual, result)
			}
			if tc.wantContains != "" {
				assert.Contains(t, result, tc.wantContains)
			}
			if tc.wantNoContains != "" {
				assert.NotContains(t, result, tc.wantNoContains)
			}
		})
	}
}

func TestSecretEquals(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantContains   string
		wantNoContains string
		wantEqual      string
	}{
		{
			name:           "match",
			input:          "secret=topsecretvalue123",
			wantContains:   "secret=[REDACTED]",
			wantNoContains: "topsecretvalue123",
		},
		{
			name:      "no match",
			input:     "This is not a secret",
			wantEqual: "This is not a secret",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := redact.Redact(tc.input)
			require.NoError(t, err)
			if tc.wantEqual != "" {
				assert.Equal(t, tc.wantEqual, result)
			}
			if tc.wantContains != "" {
				assert.Contains(t, result, tc.wantContains)
			}
			if tc.wantNoContains != "" {
				assert.NotContains(t, result, tc.wantNoContains)
			}
		})
	}
}

func TestAPIKeyEquals(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantContains   string
		wantNoContains string
		wantEqual      string
	}{
		{
			name:           "underscore match",
			input:          "api_key=abcdef123456secretkey",
			wantContains:   "[REDACTED]",
			wantNoContains: "abcdef123456secretkey",
		},
		{
			name:           "hyphen match",
			input:          "api-key=abcdef123456secretkey",
			wantContains:   "[REDACTED]",
			wantNoContains: "abcdef123456secretkey",
		},
		{
			name:      "no match",
			input:     "No API credentials here",
			wantEqual: "No API credentials here",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := redact.Redact(tc.input)
			require.NoError(t, err)
			if tc.wantEqual != "" {
				assert.Equal(t, tc.wantEqual, result)
			}
			if tc.wantContains != "" {
				assert.Contains(t, result, tc.wantContains)
			}
			if tc.wantNoContains != "" {
				assert.NotContains(t, result, tc.wantNoContains)
			}
		})
	}
}

func TestXAPIKey(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantContains   string
		wantNoContains string
		wantEqual      string
	}{
		{
			name:           "match",
			input:          "x-api-key: sk-abcdefghijklmnopqrstuvwxyz",
			wantContains:   "[REDACTED]",
			wantNoContains: "sk-abcdefghijklmnopqrstuvwxyz",
		},
		{
			name:      "no match",
			input:     "No x-api-key header present",
			wantEqual: "No x-api-key header present",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := redact.Redact(tc.input)
			require.NoError(t, err)
			if tc.wantEqual != "" {
				assert.Equal(t, tc.wantEqual, result)
			}
			if tc.wantContains != "" {
				assert.Contains(t, result, tc.wantContains)
			}
			if tc.wantNoContains != "" {
				assert.NotContains(t, result, tc.wantNoContains)
			}
		})
	}
}

func TestPEMKey(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantContains   []string
		wantNoContains string
		wantEqual      string
	}{
		{
			name:           "match single line",
			input:          "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0Z3VS5JJcds3xHn/ygWep4\n-----END RSA PRIVATE KEY-----",
			wantContains:   []string{"[REDACTED-PEM-KEY]"},
			wantNoContains: "MIIEowIBAAKCAQEA0Z3VS5JJcds3xHn/ygWep4",
		},
		{
			name:           "match multiline with surrounding text",
			input:          "Some text before\n-----BEGIN EC PRIVATE KEY-----\nABCDEFGHIJKLMNOP\nQRSTUVWXYZ123456\n-----END EC PRIVATE KEY-----\nSome text after",
			wantContains:   []string{"[REDACTED-PEM-KEY]", "Some text before", "Some text after"},
			wantNoContains: "ABCDEFGHIJKLMNOP",
		},
		{
			name:      "no match public key",
			input:     "This is a public key, not a private key",
			wantEqual: "This is a public key, not a private key",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := redact.Redact(tc.input)
			require.NoError(t, err)
			if tc.wantEqual != "" {
				assert.Equal(t, tc.wantEqual, result)
			}
			for _, c := range tc.wantContains {
				assert.Contains(t, result, c)
			}
			if tc.wantNoContains != "" {
				assert.NotContains(t, result, tc.wantNoContains)
			}
		})
	}
}

func TestAGEKey(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantContains   string
		wantNoContains string
		wantEqual      string
	}{
		{
			name:           "match",
			input:          "AGE-SECRET-KEY-1QYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQ",
			wantContains:   "[REDACTED-AGE-KEY]",
			wantNoContains: "QYQSZQGPQYQSZQGP",
		},
		{
			name:      "no match short key",
			input:     "AGE-SECRET-KEY-short",
			wantEqual: "AGE-SECRET-KEY-short",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := redact.Redact(tc.input)
			require.NoError(t, err)
			if tc.wantEqual != "" {
				assert.Equal(t, tc.wantEqual, result)
			}
			if tc.wantContains != "" {
				assert.Contains(t, result, tc.wantContains)
			}
			if tc.wantNoContains != "" {
				assert.NotContains(t, result, tc.wantNoContains)
			}
		})
	}
}

func TestSKKey(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantContains   string
		wantNoContains string
		wantEqual      string
	}{
		{
			name:           "openai key match",
			input:          "key: sk-abcdABCD1234567890abcdABCD1234567890",
			wantContains:   "[REDACTED-SK-KEY]",
			wantNoContains: "sk-abcdABCD1234567890abcdABCD1234567890",
		},
		{
			name:      "no match short key",
			input:     "sk-short",
			wantEqual: "sk-short",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := redact.Redact(tc.input)
			require.NoError(t, err)
			if tc.wantEqual != "" {
				assert.Equal(t, tc.wantEqual, result)
			}
			if tc.wantContains != "" {
				assert.Contains(t, result, tc.wantContains)
			}
			if tc.wantNoContains != "" {
				assert.NotContains(t, result, tc.wantNoContains)
			}
		})
	}
}

func TestAWSKey(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantContains   string
		wantNoContains string
		wantEqual      string
	}{
		{
			name:           "match",
			input:          "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
			wantContains:   "[REDACTED-AWS-KEY]",
			wantNoContains: "AKIAIOSFODNN7EXAMPLE",
		},
		{
			name:      "no match short key",
			input:     "AKIA_TOOSHORT",
			wantEqual: "AKIA_TOOSHORT",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := redact.Redact(tc.input)
			require.NoError(t, err)
			if tc.wantEqual != "" {
				assert.Equal(t, tc.wantEqual, result)
			}
			if tc.wantContains != "" {
				assert.Contains(t, result, tc.wantContains)
			}
			if tc.wantNoContains != "" {
				assert.NotContains(t, result, tc.wantNoContains)
			}
		})
	}
}

func TestJWT(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantContains   string
		wantNoContains string
		wantEqual      string
	}{
		{
			name:           "match",
			input:          "value: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4",
			wantContains:   "[REDACTED-JWT]",
			wantNoContains: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
		},
		{
			name:      "no match short value",
			input:     "ey.short",
			wantEqual: "ey.short",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := redact.Redact(tc.input)
			require.NoError(t, err)
			if tc.wantEqual != "" {
				assert.Equal(t, tc.wantEqual, result)
			}
			if tc.wantContains != "" {
				assert.Contains(t, result, tc.wantContains)
			}
			if tc.wantNoContains != "" {
				assert.NotContains(t, result, tc.wantNoContains)
			}
		})
	}
}

func TestAuthHeader(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantContains   string
		wantNoContains string
		wantEqual      string
	}{
		{
			name:           "match",
			input:          "authorization: BasicABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnop",
			wantContains:   "[REDACTED]",
			wantNoContains: "BasicABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnop",
		},
		{
			name:      "no match",
			input:     "No authorization header here",
			wantEqual: "No authorization header here",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := redact.Redact(tc.input)
			require.NoError(t, err)
			if tc.wantEqual != "" {
				assert.Equal(t, tc.wantEqual, result)
			}
			if tc.wantContains != "" {
				assert.Contains(t, result, tc.wantContains)
			}
			if tc.wantNoContains != "" {
				assert.NotContains(t, result, tc.wantNoContains)
			}
		})
	}
}

func TestLongBase64(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantContains string
		wantEqual    string
	}{
		{
			name:         "match long base64",
			input:        "data: ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/==",
			wantContains: "[REDACTED-BASE64]",
		},
		{
			name:      "no match short string",
			input:     "Short: ABC123",
			wantEqual: "Short: ABC123",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := redact.Redact(tc.input)
			require.NoError(t, err)
			if tc.wantEqual != "" {
				assert.Equal(t, tc.wantEqual, result)
			}
			if tc.wantContains != "" {
				assert.Contains(t, result, tc.wantContains)
			}
		})
	}
}

func TestMultiplePatterns_Applied(t *testing.T) {
	input := "url=https://user:pass@host.com token=abc123 password=secret"
	result, err := redact.Redact(input)
	require.NoError(t, err)
	assert.NotContains(t, result, ":pass@")
	assert.Contains(t, result, "[REDACTED]")
}

func TestNewRedactor_InvalidRegex(t *testing.T) {
	extra := []redact.Pattern{
		{Regex: `[invalid(regex`, Replacement: "[REDACTED]"},
	}
	_, err := redact.NewRedactor(extra)
	assert.Error(t, err)
}

func TestNewRedactor_ExtraPatterns(t *testing.T) {
	tests := []struct {
		name           string
		extra          []redact.Pattern
		input          string
		wantContains   string
		wantNoContains string
	}{
		{
			name: "extra pattern applied",
			extra: []redact.Pattern{
				{Regex: `CUSTOM-SECRET-[A-Z0-9]+`, Replacement: "[REDACTED-CUSTOM]"},
			},
			input:          "value: CUSTOM-SECRET-ABC123DEF456",
			wantContains:   "[REDACTED-CUSTOM]",
			wantNoContains: "CUSTOM-SECRET-ABC123DEF456",
		},
		{
			name: "extra pattern does not affect defaults",
			extra: []redact.Pattern{
				{Regex: `CUSTOM-SECRET-[A-Z0-9]+`, Replacement: "[REDACTED-CUSTOM]"},
			},
			input:        "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
			wantContains: "[REDACTED-AWS-KEY]",
		},
		{
			name:         "nil extra patterns uses only defaults",
			extra:        nil,
			input:        "password=mysecretvalue",
			wantContains: "[REDACTED]",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := redact.NewRedactor(tc.extra)
			require.NoError(t, err)
			result, err := r.Redact(tc.input)
			require.NoError(t, err)
			if tc.wantContains != "" {
				assert.Contains(t, result, tc.wantContains)
			}
			if tc.wantNoContains != "" {
				assert.NotContains(t, result, tc.wantNoContains)
			}
		})
	}
}

func TestNewRedactorFromFile(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(dir string) string
		wantErr     bool
		checkResult func(t *testing.T, r *redact.Redactor)
	}{
		{
			name: "path does not exist returns default redactor",
			setup: func(dir string) string {
				return dir + "/nonexistent.json"
			},
			wantErr: false,
			checkResult: func(t *testing.T, r *redact.Redactor) {
				result, err := r.Redact("password=secret123")
				require.NoError(t, err)
				assert.Contains(t, result, "[REDACTED]")
				assert.NotContains(t, result, "secret123")
			},
		},
		{
			name: "valid JSON with extra pattern applies pattern on top of defaults",
			setup: func(dir string) string {
				path := dir + "/patterns.json"
				content := `[{"regex":"MYAPP-KEY-[A-Z0-9]+","replacement":"[REDACTED-MYAPP]"}]`
				require.NoError(t, os.WriteFile(path, []byte(content), 0600))
				return path
			},
			wantErr: false,
			checkResult: func(t *testing.T, r *redact.Redactor) {
				result, err := r.Redact("MYAPP-KEY-ABC123XYZ")
				require.NoError(t, err)
				assert.Contains(t, result, "[REDACTED-MYAPP]")
				assert.NotContains(t, result, "MYAPP-KEY-ABC123XYZ")

				result2, err := r.Redact("password=defaulttest")
				require.NoError(t, err)
				assert.Contains(t, result2, "[REDACTED]")
				assert.NotContains(t, result2, "defaulttest")
			},
		},
		{
			name: "malformed JSON returns error",
			setup: func(dir string) string {
				path := dir + "/bad.json"
				require.NoError(t, os.WriteFile(path, []byte(`not json`), 0600))
				return path
			},
			wantErr: true,
			checkResult: func(t *testing.T, r *redact.Redactor) {
				assert.Nil(t, r)
			},
		},
		{
			name: "empty JSON array returns default redactor",
			setup: func(dir string) string {
				path := dir + "/empty.json"
				require.NoError(t, os.WriteFile(path, []byte(`[]`), 0600))
				return path
			},
			wantErr: false,
			checkResult: func(t *testing.T, r *redact.Redactor) {
				result, err := r.Redact("token=abc123secret")
				require.NoError(t, err)
				assert.Contains(t, result, "[REDACTED]")
				assert.NotContains(t, result, "abc123secret")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := tc.setup(dir)
			r, err := redact.NewRedactorFromFile(path)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			tc.checkResult(t, r)
		})
	}
}
