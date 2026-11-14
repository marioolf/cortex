package importer

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/marioolf/cortex/internal/session"
)

// zipOf writes a zip whose files are produced by the given writers. The
// content is generated while compressing, so the file stays tiny on disk.
func zipOf(t *testing.T, files map[string]func(w *bytes.Buffer)) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "export.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, gen := range files {
		w, _ := zw.Create(name)
		var b bytes.Buffer
		gen(&b)
		w.Write(b.Bytes())
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return p
}

func allocated(f func()) uint64 {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

func claudeJSON(uuid, text string) string {
	return fmt.Sprintf(`{"uuid":%q,"name":"n","chat_messages":[{"sender":"human","text":%q,"created_at":"2025-03-01T10:00:00Z"}]}`, uuid, text)
}

type collected struct {
	ids     []string
	skipped []string
}

func each(t *testing.T, p string, opt Options) (collected, int, error) {
	t.Helper()
	var c collected
	opt.Skip = func(r string) { c.skipped = append(c.skipped, r) }
	n, err := EachWith(p, opt, func(s *session.Session) error { c.ids = append(c.ids, s.ID); return nil })
	return c, n, err
}

// Attack 1: whitespace padding. encoding/json's Decoder buffers it all.
func TestWhitespacePaddingIsRefusedWithoutBuffering(t *testing.T) {
	const cap = 1 << 20
	for name, gen := range map[string]func(w *bytes.Buffer){
		"before the first element": func(w *bytes.Buffer) {
			w.WriteString("[")
			w.Write(bytes.Repeat([]byte(" "), cap+cap/2))
			w.WriteString("]")
		},
		"between elements": func(w *bytes.Buffer) {
			w.WriteString("[" + claudeJSON("a", "x") + ",\n")
			w.Write(bytes.Repeat([]byte("\n"), 3*cap))
			w.WriteString(claudeJSON("b", "y") + "]")
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := zipOf(t, map[string]func(*bytes.Buffer){"conversations.json": gen})
			var err error
			var c collected
			alloc := allocated(func() { c, _, err = each(t, p, Options{MaxElement: cap}) })
			if err == nil || !strings.Contains(err.Error(), "whitespace") || !strings.Contains(err.Error(), "conversations.json") {
				t.Fatalf("err = %v", err)
			}
			if alloc > 1<<20 { // 1.5 MiB of padding must not be held
				t.Errorf("allocated %d bytes", alloc)
			}
			if name == "between elements" && len(c.ids) != 1 {
				t.Errorf("the element before the padding is kept: %v", c.ids)
			}
		})
	}
}

// Attack 2: one conversation with an enormous string. It is skipped without
// being held, and the next conversation is still read.
func TestEnormousElementIsSkippedWithoutBuffering(t *testing.T) {
	const cap = 1 << 20
	p := zipOf(t, map[string]func(*bytes.Buffer){"conversations.json": func(w *bytes.Buffer) {
		w.WriteString("[" + claudeJSON("small-1", "hello") + ",")
		w.WriteString(`{"uuid":"huge","chat_messages":[{"sender":"human","text":"`)
		w.Write(bytes.Repeat([]byte("a\\\"]}"), 2<<20)) // 8 MiB with escapes and brackets inside the string
		w.WriteString(`"}]},` + claudeJSON("small-2", "bye") + "]")
	}})
	var c collected
	var n int
	var err error
	alloc := allocated(func() { c, n, err = each(t, p, Options{MaxElement: cap}) })
	if err != nil || n != 1 || strings.Join(c.ids, ",") != "small-1,small-2" {
		t.Fatalf("ids %v unreadable %d err %v", c.ids, n, err)
	}
	if len(c.skipped) != 1 || !strings.Contains(c.skipped[0], "larger than") {
		t.Errorf("reasons %q", c.skipped)
	}
	if alloc > 4<<20 { // the element is 8 MiB; at most the cap is ever held
		t.Errorf("allocated %d bytes", alloc)
	}
}

func TestEntryAndTotalCaps(t *testing.T) {
	conv := func(w *bytes.Buffer, n int, prefix string) {
		w.WriteString("[")
		for i := 0; i < n; i++ {
			if i > 0 {
				w.WriteString(",")
			}
			w.WriteString(claudeJSON(fmt.Sprintf("%s%d", prefix, i), strings.Repeat("t", 1000)))
		}
		w.WriteString("]")
	}
	p := zipOf(t, map[string]func(*bytes.Buffer){"conversations.json": func(w *bytes.Buffer) { conv(w, 1000, "a") }})
	if _, _, err := each(t, p, Options{MaxEntry: 500 << 10}); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("declared size: %v", err)
	}
	bare := writeFile(t, "c.json", func() []byte { var b bytes.Buffer; conv(&b, 1000, "a"); return b.Bytes() }())
	if c, _, err := each(t, bare, Options{MaxEntry: 500 << 10}); !errors.Is(err, errCap) || len(c.ids) == 0 || len(c.ids) >= 1000 {
		t.Errorf("read size: %d %v", len(c.ids), err)
	}
	p = zipOf(t, map[string]func(*bytes.Buffer){
		"conversations-000.json": func(w *bytes.Buffer) { conv(w, 600, "a") },
		"conversations-001.json": func(w *bytes.Buffer) { conv(w, 600, "b") },
	})
	c, _, err := each(t, p, Options{MaxTotal: 900 << 10})
	if !errors.Is(err, errCap) || !strings.Contains(err.Error(), "conversations-001.json") || len(c.ids) <= 600 {
		t.Errorf("total: %d %v", len(c.ids), err)
	}
}

// An element ends where the structure says, whatever is inside its strings.
func TestScannerHandlesAwkwardJSON(t *testing.T) {
	text := strings.Repeat(`a"]}\\ , [{`, 30000) // crosses many read chunks
	data := "\xef\xbb\xbf \n[" + claudeJSON("one", text) + " , 42 ,\"str]\", null,[1,[2]]\n," + claudeJSON("two", "x") + "\n]trailing"
	c, n, err := each(t, writeFile(t, "c.json", []byte(data)), Options{})
	if err != nil || n != 0 || strings.Join(c.ids, ",") != "one,two" {
		t.Fatalf("%v %d %v", c.ids, n, err)
	}
	var got string
	Each(writeFile(t, "c.json", []byte(data)), func(s *session.Session) error {
		if s.ID == "one" {
			got = s.Messages[0].Text
		}
		return nil
	})
	if got != strings.TrimSpace(text) {
		t.Errorf("text damaged (%d vs %d)", len(got), len(text))
	}
	for name, bad := range map[string]string{
		"missing comma": "[" + claudeJSON("a", "x") + claudeJSON("b", "y") + "]",
		"no end":        "[" + claudeJSON("a", "x"),
		"leading comma": "[," + claudeJSON("a", "x") + "]",
	} {
		c, _, err := each(t, writeFile(t, "c.json", []byte(bad)), Options{})
		if err == nil || errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: %v", name, err)
		}
		_ = c
	}
}

func TestCallbackErrorStops(t *testing.T) {
	data := mustJSON(t, []any{claudeConv("a"), claudeConv("b")})
	stop := errors.New("stop")
	n := 0
	_, err := Each(writeFile(t, "c.json", data), func(*session.Session) error { n++; return stop })
	if err != stop || n != 1 {
		t.Fatalf("%v %d", err, n)
	}
}

// D5: no id, and an id repeated within one export.
func TestMissingAndRepeatedIDsAreUnreadable(t *testing.T) {
	noID := gptConv()
	delete(noID, "id")
	emptyID := claudeConv("")
	dup := gptConv() // same id as the first
	dup["title"] = "second one must lose"
	data := mustJSON(t, []any{gptConv(), noID, emptyID, dup, claudeConv("u9")})
	p := writeFile(t, "c.json", data)
	var titles []string
	var reasons []string
	n, err := EachWith(p, Options{Skip: func(r string) { reasons = append(reasons, r) }}, func(s *session.Session) error {
		titles = append(titles, s.ID+"/"+s.Title)
		return nil
	})
	if err != nil || n != 3 {
		t.Fatalf("n = %d err = %v", n, err)
	}
	if strings.Join(titles, "|") != "abc-123/Garden plan|u9/Draft a haiku about rain" {
		t.Fatalf("%v", titles)
	}
	joined := strings.Join(reasons, "\n")
	if strings.Count(joined, "no id") != 2 || !strings.Contains(joined, `"abc-123" already appeared`) {
		t.Errorf("reasons:\n%s", joined)
	}
	// Across the files of one zip as well: importing it twice is not needed to see the same result.
	zp := makeZip(t, map[string][]byte{"conversations-000.json": mustJSON(t, []any{gptConv()}), "conversations-001.json": mustJSON(t, []any{gptConv()})})
	if c, n, err := each(t, zp, Options{}); err != nil || n != 1 || len(c.ids) != 1 {
		t.Errorf("zip: %v %d %v", c.ids, n, err)
	}
}

// The memory bound: an export of many ordinary conversations is imported with
// a live heap that does not grow with the file.
func TestEachMemoryStaysBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("writes and reads a 50 MB export")
	}
	const conversations, textLen = 7000, 4000
	p := filepath.Join(t.TempDir(), "conversations.json")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("[")
	for i := 0; i < conversations; i++ {
		if i > 0 {
			f.WriteString(",")
		}
		c := gptConv()
		c["id"] = fmt.Sprintf("gen-%06d", i)
		c["mapping"].(m)["a2b"] = node("a2b", "web", nil, gmsg("assistant", "text", []any{strings.Repeat(fmt.Sprintf("answer %d ", i), textLen/10)}, 1700000024, nil))
		f.Write(mustJSON(t, c))
	}
	f.WriteString("]")
	f.Close()
	st, _ := os.Stat(p)
	if st.Size() < 50<<20 {
		t.Fatalf("export is only %d bytes", st.Size())
	}

	const bound = 64 << 20
	var peak uint64
	count := 0
	_, err = Each(p, func(s *session.Session) error {
		count++
		if count%250 == 0 {
			var ms runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > peak {
				peak = ms.HeapAlloc
			}
		}
		return nil
	})
	if err != nil || count != conversations {
		t.Fatalf("count %d err %v", count, err)
	}
	t.Logf("export %d MB, %d conversations, peak live heap %d MB", st.Size()>>20, count, peak>>20)
	if peak > bound {
		t.Errorf("live heap %d MB exceeds %d MB", peak>>20, bound>>20)
	}
}
