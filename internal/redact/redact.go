// Package redact removes secrets from text before it reaches the vault.
package redact

import (
	"fmt"
	"regexp"
)

const mask = "[REDACTED]"

type rule struct {
	re   *regexp.Regexp
	repl string
}

type Redactor struct {
	rules []rule
}

// Built-in rules err on the side of over-redacting: a mangled code snippet in
// a note is cheaper than a leaked credential in a synced folder.
var builtin = []rule{
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`), mask},
	{regexp.MustCompile(`\b(sk-[A-Za-z0-9_\-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_\-]{20,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9\-]{10,}|AIza[0-9A-Za-z_\-]{35})`), mask},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`), mask},
	{regexp.MustCompile(`(?i)(authorization\s*:\s*(?:bearer|basic)\s+)\S+`), "${1}" + mask},
	{regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key)(["']?\s*[:=]\s*["']?)[^\s"'=]{6,}`), "${1}${2}" + mask},
}

// New returns a Redactor with the built-in rules plus the user's extra patterns.
func New(extra []string) (*Redactor, error) {
	r := &Redactor{rules: builtin}
	for _, p := range extra {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("redact pattern %q: %w", p, err)
		}
		r.rules = append(r.rules[:len(r.rules):len(r.rules)], rule{re, mask})
	}
	return r, nil
}

func (r *Redactor) Apply(s string) string {
	for _, rule := range r.rules {
		s = rule.re.ReplaceAllString(s, rule.repl)
	}
	return s
}
