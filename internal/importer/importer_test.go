package importer

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marioolf/cortex/internal/session"
)

// SkippedError and Parse collect everything Each delivers; only tests may do
// that, the product never holds all sessions.
type SkippedError struct{ Skipped int }

func (e *SkippedError) Error() string { return "skipped" }

func Parse(path string) ([]*session.Session, error) {
	var out []*session.Session
	n, err := Each(path, func(s *session.Session) error { out = append(out, s); return nil })
	if err == nil && n > 0 {
		err = &SkippedError{n}
	}
	return out, err
}

// --- invented fixtures ---

type m = map[string]any

func node(id, parent string, children []string, msg m) m {
	return m{"id": id, "parent": parent, "children": children, "message": msg}
}

func gmsg(role string, ct string, parts []any, tm float64, extra m) m {
	r := m{"author": m{"role": role}, "create_time": tm, "recipient": "all", "status": "finished_successfully",
		"content": m{"content_type": ct, "parts": parts}, "metadata": m{}}
	for k, v := range extra {
		r[k] = v
	}
	return r
}

func gptConv() m {
	return m{
		"id": "abc-123", "title": "Garden plan", "create_time": 1700000000.5, "current_node": "a2b",
		"mapping": m{
			"root": node("root", "", []string{"sys"}, nil),
			"sys":  node("sys", "root", []string{"ctx"}, gmsg("system", "text", []any{"You are helpful"}, 1700000001, nil)),
			"ctx":  node("ctx", "sys", []string{"u1"}, gmsg("user", "user_editable_context", []any{}, 1700000002, nil)),
			"u1":   node("u1", "ctx", []string{"a1", "a1b"}, gmsg("user", "text", []any{"Which plants like shade?"}, 1700000010, nil)),
			// branch not taken (regenerated answer)
			"a1":    node("a1", "u1", []string{"u2old"}, gmsg("assistant", "text", []any{"OLD ANSWER"}, 1700000011, nil)),
			"u2old": node("u2old", "a1", nil, gmsg("user", "text", []any{"OLD FOLLOWUP"}, 1700000012, nil)),
			// current branch
			"a1b":  node("a1b", "u1", []string{"th"}, gmsg("assistant", "text", []any{"Ferns and hostas.citeturn0search1turn0search2 Also entity[\"plant\",\"Hosta\",\"genus\"] grows well."}, 1700000013, nil)),
			"th":   node("th", "a1b", []string{"hid"}, gmsg("assistant", "thoughts", []any{}, 1700000014, nil)),
			"hid":  node("hid", "th", []string{"u2"}, gmsg("assistant", "text", []any{"HIDDEN"}, 1700000015, m{"metadata": m{"is_visually_hidden_from_conversation": true}})),
			"u2":   node("u2", "hid", []string{"call"}, gmsg("user", "multimodal_text", []any{"Here is my yard:", m{"content_type": "image_asset_pointer", "asset_pointer": "file-service://x"}, "what now?"}, 1700000020, nil)),
			"call": node("call", "u2", []string{"out"}, gmsg("assistant", "code", nil, 1700000021, m{"recipient": "python", "content": m{"content_type": "code", "language": "unknown", "text": "import math\nprint(math.pi)"}})),
			"out":  node("out", "call", []string{"web"}, gmsg("tool", "execution_output", []any{}, 1700000022, m{"content": m{"content_type": "execution_output", "text": "3.14"}})),
			"web":  node("web", "out", []string{"a2b"}, gmsg("assistant", "code", nil, 1700000023, m{"recipient": "web.run", "content": m{"content_type": "code", "text": `{"search_query":[{"q":"shade garden plants"}]}`}})),
			"a2b":  node("a2b", "web", nil, gmsg("assistant", "text", []any{"Try mulching."}, 1700000024, nil)),
		},
	}
}

func claudeConv(uuid string) m {
	return m{
		"uuid": uuid, "name": "", "created_at": "2025-03-01T10:00:00.000000Z", "updated_at": "2025-03-01T10:05:00.000000Z",
		"chat_messages": []any{
			m{"uuid": "m1", "sender": "human", "text": "Draft a haiku about rain", "created_at": "2025-03-01T10:00:00.000000Z", "content": []any{m{"type": "text", "text": "Draft a haiku about rain"}}, "attachments": []any{}, "files": []any{}},
			m{"uuid": "m2", "sender": "assistant", "text": "FALLBACK TEXT SHOULD NOT APPEAR", "created_at": "2025-03-01T10:00:05.000000Z", "content": []any{
				m{"type": "thinking", "thinking": "secret musings"},
				m{"type": "text", "text": "Let me look."},
				m{"type": "tool_use", "name": "web_search", "input": m{"query": "rain haiku examples"}},
				m{"type": "tool_result", "name": "web_search", "content": []any{m{"type": "text", "text": "RESULT BODY"}}},
				m{"type": "text", "text": "Soft rain on the roof."},
			}},
			m{"uuid": "m3", "sender": "assistant", "text": "A second part, text only", "created_at": "2025-03-01T10:00:09.000000Z"},
			m{"uuid": "m4", "sender": "human", "text": "Thanks", "created_at": "2025-03-01T10:01:00.000000Z"},
		},
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func makeZip(t *testing.T, files map[string][]byte) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, _ := zw.Create(name)
		w.Write(data)
	}
	zw.Close()
	return writeFile(t, "export.zip", buf.Bytes())
}

// --- ChatGPT ---

func checkGPT(t *testing.T, got []string) {
	t.Helper()
	want := []string{
		"user: Which plants like shade?",
		"assistant: Ferns and hostas. Also Hosta grows well.",
		"user: Here is my yard:\n\nwhat now?",
		"assistant: > `python` import math\n\n> `web` shade garden plants\n\nTry mulching.",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("messages:\n%q\nwant\n%q", got, want)
	}
}

func flat(t *testing.T, path string) (id, title, source string, msgs []string) {
	t.Helper()
	ss, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("got %d sessions", len(ss))
	}
	for _, mm := range ss[0].Messages {
		msgs = append(msgs, mm.Role+": "+mm.Text)
	}
	return ss[0].ID, ss[0].Title, ss[0].Source, msgs
}

func TestChatGPTBareAndZip(t *testing.T) {
	data := mustJSON(t, []any{gptConv()})
	for name, p := range map[string]string{
		"bare":       writeFile(t, "whatever.txt", data),
		"zip":        makeZip(t, map[string][]byte{"conversations.json": data, "user.json": []byte(`{"id":"x"}`), "chat.html": []byte("<html>")}),
		"zip-in-dir": makeZip(t, map[string][]byte{"export/conversations.json": data}),
	} {
		t.Run(name, func(t *testing.T) {
			id, title, src, msgs := flat(t, p)
			if id != "abc-123" || title != "Garden plan" || src != "chatgpt" {
				t.Fatalf("%q %q %q", id, title, src)
			}
			checkGPT(t, msgs)
		})
	}
}

func TestChatGPTTimesAndCwd(t *testing.T) {
	ss, err := Parse(writeFile(t, "c.json", mustJSON(t, []any{gptConv()})))
	if err != nil {
		t.Fatal(err)
	}
	if ss[0].Cwd != "" || ss[0].Messages[0].Time.Unix() != 1700000010 {
		t.Fatalf("%+v", ss[0].Messages[0])
	}
}

func TestChatGPTSplitFiles(t *testing.T) {
	c2 := gptConv()
	c2["id"] = "def-456"
	p := makeZip(t, map[string][]byte{
		"conversations-001.json": mustJSON(t, []any{c2}),
		"conversations-000.json": mustJSON(t, []any{gptConv()}),
		"export_manifest.json":   []byte(`{"files":[]}`),
	})
	ss, err := Parse(p)
	if err != nil || len(ss) != 2 || ss[0].ID != "abc-123" || ss[1].ID != "def-456" {
		t.Fatalf("%v %v", ss, err)
	}
}

func TestChatGPTNoCurrentNodeAndCycle(t *testing.T) {
	c := gptConv()
	delete(c, "current_node")
	ss, err := Parse(writeFile(t, "c.json", mustJSON(t, []any{c})))
	if err != nil || len(ss) != 1 || !strings.HasSuffix(ss[0].Messages[len(ss[0].Messages)-1].Text, "Try mulching.") {
		t.Fatalf("%v %v", ss, err)
	}
	cyc := m{"id": "cy", "title": "t", "current_node": "a", "mapping": m{
		"a": node("a", "b", nil, gmsg("user", "text", []any{"hi"}, 1, nil)),
		"b": node("b", "a", nil, gmsg("assistant", "text", []any{"yo"}, 2, nil)),
	}}
	ss, err = Parse(writeFile(t, "c.json", mustJSON(t, []any{cyc})))
	if err != nil || len(ss[0].Messages) != 2 {
		t.Fatalf("%v %v", ss, err)
	}
}

func TestCleanCitations(t *testing.T) {
	got := cleanCitations("Aciteturn0search1 B 【4†source】C D")
	if got != "A B C D" {
		t.Fatalf("%q", got)
	}
}

func TestEmptyTitleAndEmptyConversation(t *testing.T) {
	c := gptConv()
	c["title"] = ""
	empty := m{"id": "e", "title": "empty", "current_node": "s", "mapping": m{"s": node("s", "", nil, gmsg("system", "text", []any{"x"}, 1, nil))}}
	ss, err := Parse(writeFile(t, "c.json", mustJSON(t, []any{empty, c})))
	if err != nil || len(ss) != 1 || ss[0].Title != "Which plants like shade?" {
		t.Fatalf("%v %v", ss, err)
	}
}

// --- Claude ---

func TestClaude(t *testing.T) {
	data := mustJSON(t, []any{claudeConv("11111111-2222-3333-4444-555555555555")})
	for name, p := range map[string]string{
		"bare": writeFile(t, "x.json", data),
		"zip":  makeZip(t, map[string][]byte{"conversations.json": data, "projects.json": []byte("[]"), "users.json": []byte("[]")}),
	} {
		t.Run(name, func(t *testing.T) {
			id, title, src, msgs := flat(t, p)
			want := []string{
				"user: Draft a haiku about rain",
				"assistant: Let me look.\n\n> `web_search` rain haiku examples\n\nSoft rain on the roof.\n\nA second part, text only",
				"user: Thanks",
			}
			if id != "11111111-2222-3333-4444-555555555555" || src != "claude-ai" || title != "Draft a haiku about rain" {
				t.Fatalf("%q %q %q", id, title, src)
			}
			if strings.Join(msgs, "|") != strings.Join(want, "|") {
				t.Fatalf("%q", msgs)
			}
		})
	}
}

// --- robustness ---

func TestMalformedConversationSkipped(t *testing.T) {
	bad := m{"id": "bad", "mapping": "not an object"}
	data := mustJSON(t, []any{gptConv(), bad, claudeConv("u2")})
	ss, err := Parse(writeFile(t, "c.json", data))
	var sk *SkippedError
	if !errors.As(err, &sk) || sk.Skipped != 1 {
		t.Fatalf("err = %v", err)
	}
	if len(ss) != 2 || ss[0].ID != "abc-123" || ss[1].ID != "u2" {
		t.Fatalf("%v", ss)
	}
}

func TestTruncatedJSONKeepsGood(t *testing.T) {
	data := mustJSON(t, []any{gptConv(), gptConv()})
	ss, err := Parse(writeFile(t, "c.json", data[:len(data)-200]))
	if err == nil || len(ss) != 1 {
		t.Fatalf("%d %v", len(ss), err)
	}
}

func TestUnsupported(t *testing.T) {
	for name, content := range map[string][]byte{
		"object": []byte(`{"a":1}`),
		"text":   []byte("hello"),
		"others": []byte(`[{"a":1},{"b":2}]`),
		"empty":  nil,
	} {
		_, err := Parse(writeFile(t, "f.json", content))
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: %v", name, err)
		}
	}
	_, err := Parse(makeZip(t, map[string][]byte{"photos/a.json": []byte("[]")}))
	if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "ChatGPT") || !strings.Contains(err.Error(), "claude.ai") {
		t.Errorf("zip: %v", err)
	}
	ss, err := Parse(writeFile(t, "f.json", []byte("[]")))
	if err != nil || len(ss) != 0 {
		t.Errorf("empty array: %v %v", ss, err)
	}
}

func TestZipIgnoresNonConversationJSON(t *testing.T) {
	// A file named like a conversation list but under another name is not read.
	p := makeZip(t, map[string][]byte{"other.json": mustJSON(t, []any{gptConv()})})
	if _, err := Parse(p); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}

// A zip whose compressed data is corrupt is a damaged zip, not "unsupported".
func TestCorruptZipIsReportedAsDamaged(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("conversations.json")
	w.Write(mustJSON(t, []any{gptConv()}))
	zw.Close()
	data := buf.Bytes()
	data[30+len("conversations.json")] = 0x07 // a final block of the reserved type: invalid deflate data
	_, err := Parse(writeFile(t, "export.zip", data))
	if err == nil || errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "damaged zip") {
		t.Errorf("%v", err)
	}
	// And a file that only starts like a zip.
	_, err = Parse(writeFile(t, "bad.zip", []byte("PK\x03\x04 not really")))
	if err == nil || errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "damaged zip") {
		t.Errorf("%v", err)
	}
}
