package validation

import (
	"strings"
	"testing"
)

func TestValidateSecretName_Valid(t *testing.T) {
	cases := []string{
		"a",
		"my-key",
		"my_key",
		"my.key",
		"mykey123",
		"github-terraform",
		"github.terraform",
		"github_terraform",
		"a.b.c-d_e",
		"123",
		"z",
	}
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			if err := ValidateSecretName(tc); err != nil {
				t.Errorf("ValidateSecretName(%q) = %v, want nil", tc, err)
			}
		})
	}
}

func TestValidateSecretName_Invalid(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"space", "github terraform"},
		{"leading-space", " github"},
		{"trailing-space", "github "},
		{"tab", "github\tterraform"},
		{"newline", "github\nterraform"},
		{"uppercase", "Github"},
		{"uppercase-mixed", "github-Terraform"},
		{"leading-dot", ".github"},
		{"leading-hyphen", "-github"},
		{"slash", "github/terraform"},
		{"backslash", "github\\terraform"},
		{"colon", "github:terraform"},
		{"semicolon", "github;terraform"},
		{"at-sign", "github@terraform"},
		{"exclamation", "github!terraform"},
		{"tilda", "github~terraform"},
		{"caret", "github^terraform"},
		{"percent", "github%terraform"},
		{"dollar", "github$terraform"},
		{"pipe", "github|terraform"},
		{"angle-bracket", "github<terraform"},
		{"curly-brace", "github{terraform}"},
		{"parens", "github(terraform)"},
		{"bracket", "github[terraform]"},
		{"question", "github?terraform"},
		{"star", "github*terraform"},
		{"plus", "github+terraform"},
		{"equals", "github=terraform"},
		{"comma", "github,terraform"},
		{"quote", "github'terraform"},
		{"double-quote", `github"terraform`},
		{"backtick", "github`terraform"},
		{"emoji", "github🔑terraform"},
		{"unicode", "githubñterraform"},
		{"path-traversal-dotdot", "../../etc/cron.d/evil"},
		{"path-traversal-abs", "/etc/shadow"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateSecretName(tc.input); err == nil {
				t.Errorf("ValidateSecretName(%q) = nil, want error", tc.input)
			}
		})
	}
}

func TestValidateSecretName_Overlong(t *testing.T) {
	input := strings.Repeat("a", 256)
	if err := ValidateSecretName(input); err == nil {
		t.Errorf("ValidateSecretName(256-char) = nil, want error")
	}

	input = strings.Repeat("a", 255)
	if err := ValidateSecretName(input); err != nil {
		t.Errorf("ValidateSecretName(255-char) = %v, want nil", err)
	}
}

func TestSecretNameRE_CompilesAndExported(t *testing.T) {
	if SecretNameRE == nil {
		t.Fatal("SecretNameRE is nil")
	}
	if SecretNamePattern == "" {
		t.Fatal("SecretNamePattern is empty")
	}
	// Verify the exported regex matches the expected pattern
	if !SecretNameRE.MatchString("valid-name") {
		t.Error("SecretNameRE does not match 'valid-name'")
	}
	if SecretNameRE.MatchString("INVALID") {
		t.Error("SecretNameRE matches 'INVALID' (uppercase)")
	}
	if SecretNameRE.MatchString("has space") {
		t.Error("SecretNameRE matches 'has space'")
	}
}

func TestValidateSecretName_ConcreteRegression(t *testing.T) {
	// This is the exact name that triggered the bug: spaces around a dash
	if err := ValidateSecretName("github - terraform"); err == nil {
		t.Error("ValidateSecretName('github - terraform') = nil, want error (spaces)")
	}
}
