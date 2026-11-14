package redact

import (
	"strings"
	"testing"
)

func TestApply(t *testing.T) {
	r, err := New([]string{`internal-\d+`})
	if err != nil {
		t.Fatal(err)
	}
	// Every secret below is made up.
	secrets := []string{
		"sk-abcdefghijklmnopqrstuvwx",
		"ghp_abcdefghijklmnopqrstuvwxyz0123",
		"AKIAABCDEFGHIJKLMNOP",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghijklmnop",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nabc\ndef\n-----END OPENSSH PRIVATE KEY-----",
		"hunter2hunter2",
		"internal-42",
	}
	in := "key " + secrets[0] + " and " + secrets[1] + " and " + secrets[2] +
		"\nAuthorization: Bearer " + secrets[3] + "\n" + secrets[4] +
		"\npassword = \"" + secrets[5] + "\"\nticket " + secrets[6]
	out := r.Apply(in)
	for _, s := range secrets {
		if strings.Contains(out, s) {
			t.Errorf("secret survived: %q\n%s", s, out)
		}
	}
	for _, keep := range []string{"Authorization: Bearer [REDACTED]", `password = "[REDACTED]`, "ticket [REDACTED]"} {
		if !strings.Contains(out, keep) {
			t.Errorf("missing %q in\n%s", keep, out)
		}
	}
}

func TestLeavesOrdinaryTextAlone(t *testing.T) {
	r, _ := New(nil)
	for _, s := range []string{
		"token := next()",
		"the password is wrong",
		"skip the task-runner step",
		"func secret() {}",
	} {
		if got := r.Apply(s); got != s {
			t.Errorf("Apply(%q) = %q", s, got)
		}
	}
}

func TestBadPattern(t *testing.T) {
	if _, err := New([]string{"("}); err == nil {
		t.Error("expected an error for an invalid pattern")
	}
}
