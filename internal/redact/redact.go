// Package redact removes secrets from text before it reaches the vault.
package redact

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const mask = "[REDACTED]"

// rule replaces matches of re. With fn set, fn decides per match: it returns
// the replacement, or false to keep the match as it was. RE2 has no lookahead,
// so exceptions ("this value is a type name, not a secret") live in Go code.
type rule struct {
	re   *regexp.Regexp
	repl string
	fn   func(s string, m []int) (string, bool)
}

type Redactor struct {
	rules []rule
}

// kw are the words that mark a name as holding a secret. pwd is not among
// them: PWD and OLDPWD are directories in every environment dump.
const kw = `(?:password|passwd|passphrase|secret|token|api[_-]?key|[_-]pwd)`

// keySuffix: AWS_SECRET_ACCESS_KEY, token_value... A keyword that is only the
// start of a longer word (tokenizer, max_tokens) is deliberately not a key.
const keySuffix = `(?:[_-](?:access[_-]?key|key|value|string))?`

// value is a double-quoted, single-quoted or bare value (groups n, n+1, n+2).
const value = `(?:"([^"\n]{6,})"|'([^'\n]{6,})'|([^\s"'=]{6,}))`

// Built-in rules err on the side of over-redacting: a mangled code snippet in
// a note is cheaper than a leaked credential in a synced folder.
var builtin = []rule{
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`), mask, nil},
	{regexp.MustCompile(`\b(sk-[A-Za-z0-9_\-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_\-]{20,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9\-]{10,}|AIza[0-9A-Za-z_\-]{35})`), mask, nil},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`), mask, nil},
	// Authorization: the key may be quoted (JSON, Python dicts), the separator
	// : or =, the scheme open-ended (Bearer, Basic, token, Digest...) or absent.
	// Groups: 1 prefix, 2 scheme+value, 3 value after a scheme, 4 bare value.
	{regexp.MustCompile(`(?i)(authorization["']?\s*[:=]\s*["']?)(?:(?:bearer|basic|token|digest|negotiate)\s+([^\s"']{6,})|([^\s"']{6,}))`), "", authorization},
	// name: value, name=value, "name": "value". \b cannot be used before the
	// keyword (it does not match between "_" and a letter, so DB_PASSWORD would
	// be missed); the key may instead start with any identifier characters.
	// Groups: 1 key, 2 separator, 3-5 value.
	{regexp.MustCompile(`(?i)([A-Za-z0-9_.\-]*` + kw + keySuffix + `)(["']?\s*[:=]\s*)` + value), "", keyValue},
	// --password value, --token value
	{regexp.MustCompile(`(?i)(--[a-z0-9_-]*` + kw + keySuffix + `[ \t]+)` + `(?:"([^"\n]{6,})"|'([^'\n]{6,})'|([^\s"'=-][^\s"'=]{5,}))`), "", flagValue},
	// machine example.com login bob password secret (.netrc)
	{regexp.MustCompile(`(?i)(\bmachine\s+\S+\s+(?:login\s+\S+\s+)?password\s+)\S{6,}`), "${1}" + mask, nil},
	// scheme://user:password@host
	{regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.\-]*://[^\s/:@]+:)([^\s/@]+)(@)`), "", urlPassword},
	// curl -u user:password
	{regexp.MustCompile(`(?i)((?:^|\s)(?:-u|--user)[ \t]+["']?[^\s:"']+:)([^\s"']+)`), "", urlPassword},
}

func authorization(s string, m []int) (string, bool) {
	if m[4] >= 0 { // there is a scheme: whatever follows is a credential
		return s[m[0]:m[4]] + mask + s[m[5]:m[1]], true
	}
	// No scheme: "Authorization: required for all endpoints" is prose.
	if nonSecret(s[m[6]:m[7]], false) {
		return "", false
	}
	return s[m[0]:m[6]] + mask + s[m[7]:m[1]], true
}

func keyValue(s string, m []int) (string, bool) { return valueGroups(s, m, 6, true) }

func flagValue(s string, m []int) (string, bool) {
	// The flag regexp has no key and separator groups: 1 prefix, 2-4 value.
	return valueGroups(s, m, 4, false)
}

// valueGroups masks the first matched value group at or after group index
// first/2 (the three alternatives are consecutive). first is the byte offset
// into m of the first alternative.
func valueGroups(s string, m []int, first int, checkProse bool) (string, bool) {
	for g := first; g < first+6; g += 2 {
		if m[g] < 0 {
			continue
		}
		v := s[m[g]:m[g+1]]
		quoted := g < first+4
		if nonSecret(v, quoted) {
			return "", false
		}
		if checkProse && !quoted && isProse(s[m[4]:m[5]], v, s[m[1]:]) {
			return "", false
		}
		return s[m[0]:m[g]] + mask + s[m[g+1]:m[1]], true
	}
	return "", false
}

// A short all-lowercase word: what prose looks like, and what a password
// rarely is.
var word = regexp.MustCompile(`^[a-z]{1,10}$`)

// isProse spots "The secret: everything is local": a plain word after a colon,
// with more words following on the same line.
func isProse(sep, v, rest string) bool {
	if !strings.Contains(sep, ":") || !strings.HasSuffix(sep, " ") || !word.MatchString(v) {
		return false
	}
	words := strings.TrimLeft(rest, " \t")
	return len(words) < len(rest) && words != "" && unicode.IsLetter(rune(words[0]))
}

func urlPassword(s string, m []int) (string, bool) {
	if nonSecret(s[m[4]:m[5]], false) {
		return "", false
	}
	return s[m[0]:m[4]] + mask + s[m[5]:m[1]], true
}

var typeWords = map[string]bool{
	"string": true, "str": true, "number": true, "int": true, "bool": true, "boolean": true,
	"bytes": true, "any": true, "object": true, "none": true, "null": true, "nil": true,
	"undefined": true, "true": true, "false": true, "required": true, "optional": true,
}

var typePrefixes = []string{"optional[", "union[", "list[", "dict[", "option<", "vec<", "promise<", "array<"}

// dottedPath is an identifier path such as self.token or process.env.TOKEN. A
// secret that looks like one has at least a digit in it.
var dottedPath = regexp.MustCompile(`^[A-Za-z_][A-Za-z_]*(\.[A-Za-z_]+)+$`)

// The exceptions below are deliberately narrow: each one is a shape that a
// generated password can also have, so anything that is not unmistakably a
// reference or code gets redacted.
var (
	// ${VAR}, $(cmd), $VAR, %VAR%, <your-token>. Passwords do start with $ or %,
	// so a bare $name must be all upper or all lower case to count as a variable.
	reference = regexp.MustCompile(`^(\$\{.*|\$\(.*|\$[A-Z_][A-Z0-9_]*|%[A-Z_][A-Z0-9_]*%|<[A-Za-z][A-Za-z _-]*>)$`)
	shellVar  = regexp.MustCompile(`^\$[a-z_][a-z0-9_]*$`)
	// getToken(), load(x), args[1], and os.environ[ / os.getenv( cut short by
	// the quote that follows.
	codeExpr = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*(\([^()]*\)[;,)]*|\[\d+\][;,)]*|[(\[])$`)
)

// nonSecret reports whether v looks like a type, keyword, variable reference or
// code rather than a literal secret. Quoted values are literals, so the
// code-shaped checks apply to bare values only.
func nonSecret(v string, quoted bool) bool {
	t := strings.TrimRight(v, ");,:}]>.")
	l := strings.ToLower(v)
	if typeWords[strings.ToLower(t)] || reference.MatchString(v) {
		return true
	}
	if quoted {
		return false
	}
	for _, p := range typePrefixes {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	u := strings.TrimRight(v, ";,")
	return shellVar.MatchString(u) || dottedPath.MatchString(t) || codeExpr.MatchString(u)
}

// New returns a Redactor with the built-in rules plus the user's extra patterns.
func New(extra []string) (*Redactor, error) {
	r := &Redactor{rules: builtin}
	for _, p := range extra {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("redact pattern %q: %w", p, err)
		}
		r.rules = append(r.rules[:len(r.rules):len(r.rules)], rule{re: re, repl: mask})
	}
	return r, nil
}

func (r *Redactor) Apply(s string) string {
	for _, rule := range r.rules {
		if rule.fn == nil {
			s = rule.re.ReplaceAllString(s, rule.repl)
		} else {
			s = replaceFunc(rule, s)
		}
	}
	return s
}

// replaceFunc is ReplaceAllString with a per-match decision.
func replaceFunc(r rule, s string) string {
	var b strings.Builder
	last := 0
	for _, m := range r.re.FindAllStringSubmatchIndex(s, -1) {
		if repl, ok := r.fn(s, m); ok {
			b.WriteString(s[last:m[0]])
			b.WriteString(repl)
			last = m[1]
		}
	}
	b.WriteString(s[last:])
	return b.String()
}
