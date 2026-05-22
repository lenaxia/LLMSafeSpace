package redact

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"
)

const defaultConfigPath = "/sandbox-cfg/redact-patterns.json"

type Pattern struct {
	Regex       string `json:"regex"`
	Replacement string `json:"replacement"`
}

type compiledPattern struct {
	re          *regexp.Regexp
	replacement string
}

type Redactor struct {
	patterns []compiledPattern
}

var defaultPatterns = []Pattern{
	{`(?i)://[^:@\s]*:[^@\s]+@`, `://[REDACTED]@`},
	{`(?i)(bearer )\S+`, `${1}[REDACTED]`},
	{`gh[a-z]_[A-Za-z0-9]{36,}`, `[REDACTED-GH-TOKEN]`},
	{`(?i)("password"\s*:\s*)"[^"]*"`, `${1}"[REDACTED]"`},
	{`(?i)(password\s*[=:]\s*)\S+`, `${1}[REDACTED]`},
	{`(?i)(token\s*[=:]\s*)\S+`, `${1}[REDACTED]`},
	{`(?i)(secret\s*[=:]\s*)\S+`, `${1}[REDACTED]`},
	{`(?i)(api[_-]?key\s*[=:]\s*)\S+`, `${1}[REDACTED]`},
	{`(?i)(x-api-key\s*[=:]\s*)\S+`, `${1}[REDACTED]`},
	{`(?is)-----BEGIN .*PRIVATE KEY-----.*?-----END .*PRIVATE KEY-----`, `[REDACTED-PEM-KEY]`},
	{`(?i)AGE-SECRET-KEY-1[A-Z0-9]{40,}`, `[REDACTED-AGE-KEY]`},
	{`sk-[a-zA-Z0-9_\-]{4,}[A-Za-z0-9]{16,}`, `[REDACTED-SK-KEY]`},
	{`AKIA[A-Z0-9]{16}`, `[REDACTED-AWS-KEY]`},
	{`ey[A-Za-z0-9_\-]{10,}\.ey[A-Za-z0-9_\-]{10,}`, `[REDACTED-JWT]`},
	{`(?i)(authorization\s*:\s*)\S+`, `${1}[REDACTED]`},
	{`[A-Za-z0-9+/]{40,}={0,2}`, `[REDACTED-BASE64]`},
}

var defaultRedactor = mustNewRedactorFromPatterns(defaultPatterns)

func mustNewRedactorFromPatterns(patterns []Pattern) *Redactor {
	r, err := newRedactorFromPatterns(patterns)
	if err != nil {
		panic(fmt.Sprintf("redact: failed to compile built-in patterns: %v", err))
	}
	return r
}

func newRedactorFromPatterns(patterns []Pattern) (*Redactor, error) {
	compiled := make([]compiledPattern, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p.Regex)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", p.Regex, err)
		}
		compiled = append(compiled, compiledPattern{re: re, replacement: p.Replacement})
	}
	return &Redactor{patterns: compiled}, nil
}

func NewRedactor(extraPatterns []Pattern) (*Redactor, error) {
	all := make([]Pattern, len(defaultPatterns), len(defaultPatterns)+len(extraPatterns))
	copy(all, defaultPatterns)
	all = append(all, extraPatterns...)
	return newRedactorFromPatterns(all)
}

func (r *Redactor) Redact(input string) (string, error) {
	result := input
	for _, p := range r.patterns {
		result = p.re.ReplaceAllString(result, p.replacement)
	}
	return result, nil
}

var (
	cachedRedactor     *Redactor
	cachedRedactorOnce sync.Once
	cachedRedactorErr  error
)

func Redact(input string) (string, error) {
	cachedRedactorOnce.Do(func() {
		cachedRedactor, cachedRedactorErr = NewRedactorFromFile(defaultConfigPath)
	})
	if cachedRedactorErr != nil {
		return "", cachedRedactorErr
	}
	return cachedRedactor.Redact(input)
}

func NewRedactorFromFile(path string) (*Redactor, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewRedactor(nil)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read config %s: %w", path, err)
	}

	var extra []Pattern
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, fmt.Errorf("malformed config %s: %w", path, err)
	}

	return NewRedactor(extra)
}
