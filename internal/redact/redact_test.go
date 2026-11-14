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

func TestCommonForms(t *testing.T) {
	r, _ := New(nil)
	cases := []struct{ in, want string }{
		{"DB_PASSWORD=ENVPWVALfake31", "DB_PASSWORD=[REDACTED]"},
		{"OPENAI_API_KEY=ENVAPIKEYfake64", "OPENAI_API_KEY=[REDACTED]"},
		{"AWS_SECRET_ACCESS_KEY=abcdEFGH1234abcdEFGH1234", "AWS_SECRET_ACCESS_KEY=[REDACTED]"},
		{"GITHUB_TOKEN=abcdefgh12345678", "GITHUB_TOKEN=[REDACTED]"},
		{`export MY_SECRET="abcdefgh1234"`, `export MY_SECRET="[REDACTED]"`},
		{`{"Authorization": "Bearer Xabcdefgh12345678"}`, `{"Authorization": "Bearer [REDACTED]"}`},
		{`{'Authorization': 'Bearer Xabcdefgh12345678'}`, `{'Authorization': 'Bearer [REDACTED]'}`},
		{"Authorization: token Xabcdefgh12345678", "Authorization: token [REDACTED]"},
		{`"apiKey": "abcdefgh12345678"`, `"apiKey": "[REDACTED]"`},
		{"client_secret: abcdefgh12345678", "client_secret: [REDACTED]"},
		{"x-api-key: abcdefgh12345678", "x-api-key: [REDACTED]"},
		{"--password hunter2hunter2", "--password [REDACTED]"},
		{"--token abcdefgh12345678", "--token [REDACTED]"},
		{"machine example.com login bob password hunter2hunter2", "machine example.com login bob password [REDACTED]"},
		{`password: "correct horse battery staple"`, `password: "[REDACTED]"`},
		{"passphrase: abcdefgh12345678", "passphrase: [REDACTED]"},
		{"https://bob:hunter2hunter2@example.com/repo.git", "https://bob:[REDACTED]@example.com/repo.git"},
		{"curl -u bob:hunter2hunter2 https://example.com", "curl -u bob:[REDACTED] https://example.com"},
		{"Authorization=Bearer abcdefgh12345678", "Authorization=Bearer [REDACTED]"},
	}
	for _, c := range cases {
		if got := r.Apply(c.in); got != c.want {
			t.Errorf("Apply(%q) = %q, want %q", c.in, got, c.want)
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
		"refreshToken: string",
		"apiKey: string",
		"token: string;",
		"password: Optional[str]",
		"def login(user, password=None):",
		"password=None):",
		"nextPageToken: response.nextPageToken",
		"OLDPWD=/home/x/src",
		"PWD=/home/x",
		"token = getToken()",
		"password: ${DB_PASSWORD}",
		`api_key=os.environ["KEY"]`,
		"Authorization: required for all endpoints",
		"The secret: everything is local",
		"token: <your-token-here>",
		"self.token = self.token",
		"git clone --token-file /etc/x https://example.com/a",
		"https://example.com:8080/path",
		// A keyword that merely starts a longer word is not a key.
		"tokenizer = build()",
		"max_tokens = 4096",
		"the token is used for auth",
		"if password == other {",
		"if token != nil {",
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

// Shapes that code and placeholders share with real passwords: when in doubt, redact.
func TestAmbiguousValuesAreRedacted(t *testing.T) {
	r, _ := New(nil)
	for _, in := range []string{
		`"password": "$ecureDOLLARfake20"`,
		`secret: "<ANGLEfake23>"`,
		`DB_PASSWORD=$ecureBAREfake21`,
		`password: %PCTfake22abc`,
		`DB_PASSWORD=Xy7(PARENfake24)Lm`,
		`API_KEY=abc[BRACKETfake25]`,
		`my password: swordfishPROSEfake then login`,
		`the db password: letmeinwords and user admin`,
		`MYSQL_PWD=hunter2hunter2`,
	} {
		got := r.Apply(in)
		if !strings.Contains(got, mask) {
			t.Errorf("Apply(%q) = %q, want it redacted", in, got)
		}
		for _, leak := range []string{"fake2", "letmeinwords", "hunter2"} {
			if strings.Contains(got, leak) {
				t.Errorf("Apply(%q) = %q leaks %q", in, got, leak)
			}
		}
	}
	for _, in := range []string{
		`password: "${DB_PASSWORD}"`,
		`token: "<your-token>"`,
		`password=$DB_PASS`,
		`password=$pass`,
		`set TOKEN=%GH_TOKEN%`,
		`token = args[1]`,
		`secret = load(path)`,
	} {
		if got := r.Apply(in); got != in {
			t.Errorf("Apply(%q) = %q, want it left alone", in, got)
		}
	}
}
