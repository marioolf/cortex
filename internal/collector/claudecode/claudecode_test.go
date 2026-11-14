package claudecode

import (
	"strings"
	"testing"
)

// Synthetic transcript covering the entry shapes the parser must handle.
const transcript = `{"type":"mode","mode":"default","sessionId":"s1"}
{"type":"user","cwd":"/work/demo","timestamp":"2026-01-02T10:00:00.000Z","message":{"role":"user","content":"<system-reminder>injected</system-reminder>How do I parse JSONL?\nSecond line"}}
{"type":"assistant","timestamp":"2026-01-02T10:00:05.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"private"},{"type":"text","text":"Read it line by line."}]}}
{"type":"assistant","timestamp":"2026-01-02T10:00:06.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls -la\necho hi","description":"List files"}}]}}
{"type":"user","timestamp":"2026-01-02T10:00:07.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"TOOL OUTPUT"}]}}
{"type":"assistant","isSidechain":true,"timestamp":"2026-01-02T10:00:08.000Z","message":{"role":"assistant","content":[{"type":"text","text":"subagent chatter"}]}}
{"type":"user","isMeta":true,"message":{"role":"user","content":"meta noise"}}
{"type":"api-request-blob","message":"not an object"}
this line is not json
{"type":"user","timestamp":"2026-01-02T10:01:00.000Z","message":{"role":"user","content":"<command-name>/compact</command-name><command-message>compact</command-message><command-args>keep it short</command-args>"}}
{"type":"ai-title","aiTitle":"Parsing JSONL"}`

func TestParse(t *testing.T) {
	s, err := Parse(strings.NewReader(transcript))
	if err != nil {
		t.Fatal(err)
	}
	if s.Source != ID || s.Cwd != "/work/demo" || s.Title != "Parsing JSONL" {
		t.Errorf("source=%q cwd=%q title=%q", s.Source, s.Cwd, s.Title)
	}
	want := []struct{ role, text string }{
		{"user", "How do I parse JSONL?\nSecond line"},
		{"assistant", "Read it line by line.\n\n> `Bash` List files"},
		{"user", "/compact keep it short"},
	}
	if len(s.Messages) != len(want) {
		t.Fatalf("got %d messages, want %d: %+v", len(s.Messages), len(want), s.Messages)
	}
	for i, w := range want {
		if m := s.Messages[i]; m.Role != w.role || m.Text != w.text {
			t.Errorf("message %d = %q %q, want %q %q", i, m.Role, m.Text, w.role, w.text)
		}
	}
	if got := s.Started().Format("15:04:05"); got != "10:00:00" {
		t.Errorf("started = %s", got)
	}
}

func TestTitleFallsBackToFirstPrompt(t *testing.T) {
	s, _ := Parse(strings.NewReader(`{"type":"user","message":{"content":"First line\nrest"}}`))
	if s.Title != "First line" {
		t.Errorf("title = %q", s.Title)
	}
}

func TestCustomTitleWins(t *testing.T) {
	s, _ := Parse(strings.NewReader(`{"type":"ai-title","aiTitle":"auto"}
{"type":"custom-title","customTitle":"mine"}
{"type":"ai-title","aiTitle":"auto again"}`))
	if s.Title != "mine" {
		t.Errorf("title = %q", s.Title)
	}
}
