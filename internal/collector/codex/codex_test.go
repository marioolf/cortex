package codex

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/marioolf/cortex/internal/session"
)

// All fixtures are invented; they follow the shapes in openai/codex (codex-rs
// rollout recorder and protocol crates).
const (
	uuid1 = "11111111-2222-3333-4444-555555555555"
	uuid2 = "66666666-7777-8888-9999-000000000000"

	// Generation 1: flat metadata line, bare response items, a state line.
	oldRollout = `{"id":"` + uuid1 + `","timestamp":"2025-05-07T17:24:21.000Z","instructions":"be brief"}
{"type":"message","role":"user","content":[{"type":"input_text","text":"<user_instructions>\n\nbe brief\n\n</user_instructions>"}]}
{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/home/ann/proj</cwd>\n  <approval_policy>on-request</approval_policy>\n</environment_context>"}]}
{"record_type":"state"}
{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the parser\nplease"}]}
{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"thinking"}]}
{"type":"function_call","name":"shell","arguments":"{\"command\":[\"bash\",\"-lc\",\"cargo test\\nsecond line\"],\"workdir\":\"/x\"}","call_id":"c1"}
{"type":"function_call_output","call_id":"c1","output":{"content":"ok","success":true}}
{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done."}]}
`
	// Generation 2/3: wrapped lines, messages logged as response_item and as
	// legacy events, plus injected AGENTS.md and environment blocks.
	newRollout = `{"timestamp":"2025-10-01T10:00:00.000Z","type":"session_meta","payload":{"id":"` + uuid2 + `","timestamp":"2025-10-01T10:00:00.000Z","cwd":"/work/app","originator":"codex_cli_rs","cli_version":"0.40.0"}}
{"timestamp":"2025-10-01T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /work/app\n\n<INSTRUCTIONS>\nuse tabs\n</INSTRUCTIONS>"},{"type":"input_text","text":"<environment_context>\n<cwd>/work/app</cwd>\n</environment_context>"}]}}
{"timestamp":"2025-10-01T10:00:02.000Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"sandbox rules"}]}}
{"timestamp":"2025-10-01T10:00:03.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"add a flag"}]}}
{"timestamp":"2025-10-01T10:00:03.000Z","type":"event_msg","payload":{"type":"user_message","message":"add a flag"}}
{"timestamp":"2025-10-01T10:00:04.000Z","type":"turn_context","payload":{"cwd":"/other"}}
{"timestamp":"2025-10-01T10:00:05.000Z","type":"response_item","payload":{"type":"local_shell_call","call_id":"c","status":"completed","action":{"type":"exec","command":["git","status"]}}}
{"timestamp":"2025-10-01T10:00:06.000Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"d","name":"apply_patch","input":"*** Begin Patch\n*** Update File: main.go\n@@\n"}}
{"timestamp":"2025-10-01T10:00:07.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"d","output":"Success"}}
{"timestamp":"2025-10-01T10:00:08.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Added."}]}}
{"timestamp":"2025-10-01T10:00:08.000Z","type":"event_msg","payload":{"type":"agent_message","message":"Added."}}
{"timestamp":"2025-10-01T10:00:09.000Z","type":"event_msg","payload":{"type":"token_count","info":null}}
`
	// Newest paginated rollouts keep typed messages only as item_completed.
	pagedRollout = `{"timestamp":"2026-01-01T10:00:00.000Z","type":"session_meta","payload":{"session_id":"` + uuid1 + `","id":"` + uuid1 + `","timestamp":"2026-01-01T10:00:00.000Z","cwd":"/p","originator":"x","cli_version":"1"}}
{"timestamp":"2026-01-01T10:00:01.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"t","turn_id":"u","item":{"type":"UserMessage","id":"i","content":[{"type":"text","text":"hello","text_elements":[]}]}}}
{"timestamp":"2026-01-01T10:00:02.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"t","turn_id":"u","item":{"type":"AgentMessage","id":"j","content":[{"type":"Text","text":"hi there"}]}}}
`
)

func write(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func parse(t *testing.T, s string) *session.Session {
	t.Helper()
	got, err := Parse(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestOldFormat(t *testing.T) {
	s := parse(t, oldRollout)
	if s.ID != uuid1 || s.Cwd != "/home/ann/proj" || s.Title != "fix the parser" {
		t.Fatalf("meta: %+v", s)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("want 2 messages, got %+v", s.Messages)
	}
	if got := s.Messages[1].Text; got != "> `shell` cargo test\n\nDone." {
		t.Errorf("assistant: %q", got)
	}
	if s.Messages[0].Time.IsZero() {
		t.Error("first message should take the metadata time")
	}
}

func TestNewFormatDedupAndStripping(t *testing.T) {
	s := parse(t, newRollout)
	if s.ID != uuid2 || s.Cwd != "/work/app" || s.Title != "add a flag" {
		t.Fatalf("meta: %+v", s)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("want 2 messages, got %+v", s.Messages)
	}
	u, a := s.Messages[0], s.Messages[1]
	if u.Text != "add a flag" || u.Time.IsZero() {
		t.Errorf("user: %+v", u)
	}
	if a.Text != "> `shell` git status\n\n> `apply_patch` *** Update File: main.go\n\nAdded." {
		t.Errorf("assistant: %q", a.Text)
	}
}

func TestPaginatedItemCompleted(t *testing.T) {
	s := parse(t, pagedRollout)
	if len(s.Messages) != 2 || s.Messages[0].Text != "hello" || s.Messages[1].Text != "hi there" {
		t.Fatalf("got %+v", s.Messages)
	}
}

func TestIDFromFileName(t *testing.T) {
	p := write(t, filepath.Join(t.TempDir(), "rollout-2025-05-07T17-24-21-"+uuid1+".jsonl"),
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}`+"\n")
	s, err := ParseFile(p)
	if err != nil || s.ID != uuid1 {
		t.Fatalf("%v %+v", err, s)
	}
}

func TestCollectSeenMalformedArchived(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	archived := filepath.Join(root, "archived_sessions")
	f1 := write(t, filepath.Join(sessions, "2025", "10", "01", "rollout-2025-10-01T10-00-00-"+uuid2+".jsonl"), newRollout)
	write(t, filepath.Join(archived, "rollout-2025-05-07T17-24-21-"+uuid1+".jsonl"), oldRollout)
	write(t, filepath.Join(sessions, "2025", "10", "01", "rollout-garbage.jsonl"), "not json\n{\"type\":\n\n")
	write(t, filepath.Join(sessions, "2025", "10", "01", "notes.jsonl"), oldRollout) // wrong name
	// A dangling link cannot be read: warn, keep going.
	wantWarn := os.Symlink(filepath.Join(root, "nowhere"), filepath.Join(sessions, "rollout-link.jsonl")) == nil

	c := &Collector{Roots: []string{sessions, archived, filepath.Join(root, "missing"), sessions}}
	if got := c.Detect(); len(got) != 2 {
		t.Fatalf("Detect = %v", got)
	}
	var ids []string
	warns := 0
	collect := func() {
		ids = nil
		c.Collect(func(s *session.Session) { ids = append(ids, s.ID) }, func(error) { warns++ })
	}
	collect()
	if len(ids) != 2 || (wantWarn && warns == 0) {
		t.Fatalf("ids=%v warns=%d", ids, warns)
	}
	collect()
	if len(ids) != 0 {
		t.Fatalf("unchanged files reported again: %v", ids)
	}
	write(t, f1, newRollout+`{"timestamp":"2025-10-01T10:01:00.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"more"}]}}`+"\n")
	collect()
	if len(ids) != 1 || ids[0] != uuid2 {
		t.Fatalf("changed file not reported: %v", ids)
	}
	c.Reset()
	collect()
	if len(ids) != 2 {
		t.Fatalf("after Reset: %v", ids)
	}
}

func TestNewRootsHonourCodexHome(t *testing.T) {
	r := homeRoots("/c", "/h")
	want := []string{"/c/sessions", "/c/archived_sessions", "/h/.codex/sessions", "/h/.codex/archived_sessions"}
	for i := range want {
		if filepath.ToSlash(r[i]) != want[i] {
			t.Fatalf("%v", r)
		}
	}
}

// writeZst stores content as Codex does: one plain zstd frame, no extra framing.
func writeZst(t *testing.T, path string, content []byte) string {
	t.Helper()
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		t.Fatal(err)
	}
	return write(t, path, string(enc.EncodeAll(content, nil)))
}

func TestCompressedRollouts(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"rollout-2025-05-07T17-24-21-" + uuid1 + ".jsonl.zst": oldRollout,
		"rollout-2025-10-01T10-00-00-" + uuid2 + ".jsonl.zst": newRollout,
	} {
		writeZst(t, filepath.Join(dir, name), []byte(content))
	}
	// Temp files of the compressor are not rollouts.
	write(t, filepath.Join(dir, "rollout-compress-abc.tmp"), "x")
	c := &Collector{Roots: []string{dir}}
	got := map[string]string{}
	warns := 0
	collect := func() {
		clear(got)
		c.Collect(func(s *session.Session) { got[s.ID] = s.Title }, func(error) { warns++ })
	}
	collect()
	if warns != 0 || got[uuid1] != "fix the parser" || got[uuid2] != "add a flag" || len(got) != 2 {
		t.Fatalf("got %v warns=%d", got, warns)
	}
	collect()
	if len(got) != 0 {
		t.Fatalf("unchanged compressed files reported again: %v", got)
	}
}

func TestCompressedIDFromName(t *testing.T) {
	p := writeZst(t, filepath.Join(t.TempDir(), "rollout-2025-05-07T17-24-21-"+uuid1+".jsonl.zst"),
		[]byte(`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}`+"\n"))
	if s, err := ParseFile(p); err != nil || s.ID != uuid1 {
		t.Fatalf("%v %+v", err, s)
	}
}

func TestPlainWinsOverCompressedSibling(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "rollout-2025-10-01T10-00-00-"+uuid2+".jsonl")
	write(t, base, newRollout)
	writeZst(t, base+".zst", []byte(oldRollout)) // stale copy: must not be used
	c := &Collector{Roots: []string{dir}}
	var titles []string
	c.Collect(func(s *session.Session) { titles = append(titles, s.Title) }, func(err error) { t.Error(err) })
	if len(titles) != 1 || titles[0] != "add a flag" {
		t.Fatalf("got %v", titles)
	}
}

func TestCorruptCompressedFileIsOnlyAWarning(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "rollout-bad.jsonl.zst"), "this is not zstd at all")
	good := writeZst(t, filepath.Join(dir, "rollout-2025-10-01T10-00-00-"+uuid2+".jsonl.zst"), []byte(newRollout))
	// A real frame cut in half.
	b, _ := os.ReadFile(good)
	write(t, filepath.Join(dir, "rollout-cut.jsonl.zst"), string(b[:len(b)/2]))

	c := &Collector{Roots: []string{dir}}
	ids, warns := 0, 0
	c.Collect(func(*session.Session) { ids++ }, func(error) { warns++ })
	if ids != 1 || warns != 2 {
		t.Fatalf("ids=%d warns=%d", ids, warns)
	}
}

func TestDecompressionBombIsCapped(t *testing.T) {
	old := maxDecompressed
	maxDecompressed = 1 << 20
	defer func() { maxDecompressed = old }()

	// 64 MiB of one line shrinks to a few KiB.
	bomb := bytes.Repeat([]byte("a"), 64<<20)
	p := writeZst(t, filepath.Join(t.TempDir(), "rollout-bomb.jsonl.zst"), bomb)
	if st, _ := os.Stat(p); st.Size() > 1<<20 {
		t.Fatalf("fixture is not small: %d", st.Size())
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	_, err := ParseFile(p)
	runtime.ReadMemStats(&after)
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
	// TotalAlloc counts everything allocated, so it bounds the work done.
	if got := after.TotalAlloc - before.TotalAlloc; got > 48<<20 {
		t.Errorf("allocated %d MiB", got>>20)
	}
}
