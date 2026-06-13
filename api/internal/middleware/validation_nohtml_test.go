package middleware

import (
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
)

func setupNoHTMLValidator(t *testing.T) *validator.Validate {
	t.Helper()
	v := validator.New()
	if err := v.RegisterValidation("nohtml", validateNoHTML); err != nil {
		t.Fatalf("register nohtml validator: %v", err)
	}
	return v
}

func TestNoHTML_Bug_UnclosedScriptTag(t *testing.T) {
	v := setupNoHTMLValidator(t)
	err := v.Var("<script>alert(1)", "nohtml")
	assert.Error(t, err, "unclosed <script> tag must be rejected")
}

func TestNoHTML_Bug_UnclosedImgTag(t *testing.T) {
	v := setupNoHTMLValidator(t)
	err := v.Var("<img src=x onerror=alert(1)", "nohtml")
	assert.Error(t, err, "unclosed <img> tag must be rejected")
}

func TestNoHTML_Bug_UnclosedDiv(t *testing.T) {
	v := setupNoHTMLValidator(t)
	err := v.Var("<div", "nohtml")
	assert.Error(t, err, "unclosed <div must be rejected")
}

func TestNoHTML_Bug_OnlyOpenAngleBracket(t *testing.T) {
	v := setupNoHTMLValidator(t)
	err := v.Var("<", "nohtml")
	assert.Error(t, err, "lone < must be rejected")
}

func TestNoHTML_Bug_OnlyCloseAngleBracket(t *testing.T) {
	v := setupNoHTMLValidator(t)
	err := v.Var(">", "nohtml")
	assert.Error(t, err, "lone > must be rejected")
}

func TestNoHTML_Bug_CloseBracketWithoutOpen(t *testing.T) {
	v := setupNoHTMLValidator(t)
	err := v.Var("1 > 2", "nohtml")
	assert.Error(t, err, "string containing > must be rejected")
}

func TestNoHTML_PlainText_Passes(t *testing.T) {
	v := setupNoHTMLValidator(t)
	assert.NoError(t, v.Var("Hello world", "nohtml"))
}

func TestNoHTML_EmptyString_Passes(t *testing.T) {
	v := setupNoHTMLValidator(t)
	assert.NoError(t, v.Var("", "nohtml"))
}

func TestNoHTML_SpecialCharsNoBrackets_Passes(t *testing.T) {
	v := setupNoHTMLValidator(t)
	assert.NoError(t, v.Var("price: $100 & free shipping!", "nohtml"))
}

func TestNoHTML_NumberAndSpaces_Passes(t *testing.T) {
	v := setupNoHTMLValidator(t)
	assert.NoError(t, v.Var("12345", "nohtml"))
}

func TestNoHTML_CompleteTag_Fails(t *testing.T) {
	v := setupNoHTMLValidator(t)
	assert.Error(t, v.Var("<b>bold</b>", "nohtml"))
}

func TestNoHTML_ClosingTagOnly_Fails(t *testing.T) {
	v := setupNoHTMLValidator(t)
	assert.Error(t, v.Var("</script>", "nohtml"))
}

func TestNoHTML_SelfClosingTag_Fails(t *testing.T) {
	v := setupNoHTMLValidator(t)
	assert.Error(t, v.Var("<br/>", "nohtml"))
}

func TestNoHTML_NestedBrackets_Fails(t *testing.T) {
	v := setupNoHTMLValidator(t)
	assert.Error(t, v.Var("<<>>", "nohtml"))
}

func TestNoHTML_OpenBracketInMiddle_Fails(t *testing.T) {
	v := setupNoHTMLValidator(t)
	assert.Error(t, v.Var("hello < world", "nohtml"))
}

func TestNoHTML_CloseBracketInMiddle_Fails(t *testing.T) {
	v := setupNoHTMLValidator(t)
	assert.Error(t, v.Var("hello > world", "nohtml"))
}

func TestNoHTML_UnicodeAngleLikeChars_Passes(t *testing.T) {
	v := setupNoHTMLValidator(t)
	assert.NoError(t, v.Var("中文测试", "nohtml"))
	assert.NoError(t, v.Var("café résumé", "nohtml"))
}

func TestNoHTML_NonStringField_Passes(t *testing.T) {
	v := setupNoHTMLValidator(t)
	assert.NoError(t, v.Var(12345, "nohtml"))
}
