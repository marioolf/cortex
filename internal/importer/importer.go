// Package importer reads the data exports that ChatGPT and claude.ai offer
// ("Export data" in their settings) and turns them into sessions.
//
// Both exports are a zip holding a large JSON array of conversations. The
// array is decoded one conversation at a time so that a multi-hundred-MB
// export never sits in memory whole, and nothing is extracted to disk.
//
// Format sources (neither format is documented officially; both were
// checked against community descriptions only, not against a live export):
//   - ChatGPT: mapping tree with current_node/parent/children, as described in
//     https://community.openai.com/t/decoding-exported-data-by-parsing-conversations-json-and-or-chat-html/403144
//     and https://ai-chat-importer.com/guides/chatgpt-export-format-explained ;
//     the split into numbered conversations-NNN.json files is mentioned by
//     https://help.openai.com/en/articles/9106926-how-to-export-your-data-from-chatgpt
//     and https://ai-chat-importer.com/blog/chatgpt-export-multiple-files-explained ;
//     the content types, recipient values and the U+E200..U+E201 citation
//     markers come from the author's knowledge of the web app's own payloads.
//   - claude.ai: conversations.json with chat_messages and content blocks.
package importer

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/marioolf/cortex/internal/session"
)

// Source IDs of the sessions this package produces.
const (
	SourceChatGPT  = "chatgpt"
	SourceClaudeAI = "claude-ai"
)

// maxEntry caps the decompressed size of one zip entry (zip bombs).
const maxEntry = 4 << 30

// ErrUnsupported means the file is not a ChatGPT or claude.ai export.
var ErrUnsupported = errors.New("unsupported file: expected a ChatGPT export (zip or conversations.json) or a claude.ai export (zip or conversations.json)")

// SkippedError reports conversations that could not be decoded. Parse returns
// it together with the conversations that did load, so callers should use the
// slice even when err != nil:
//
//	sessions, err := importer.Parse(p)
//	var sk *importer.SkippedError
//	if errors.As(err, &sk) { warn(sk) } else if err != nil { return err }
type SkippedError struct{ Skipped int }

func (e *SkippedError) Error() string {
	return fmt.Sprintf("%d conversation(s) could not be read and were skipped", e.Skipped)
}

// Parse reads an export. path may be the zip as downloaded or one of the JSON
// files inside it; the kind is detected from the content, not the name. Any
// other error (unsupported file, broken zip, truncated JSON) may also come
// with the sessions read before the failure.
func Parse(path string) ([]*session.Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var magic [4]byte
	n, _ := io.ReadFull(f, magic[:])
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if bytes.Equal(magic[:n], []byte("PK\x03\x04")) {
		return ParseZip(f, st.Size())
	}
	return ParseJSON(f)
}

// ParseJSON reads a bare conversations JSON file as a stream.
func ParseJSON(r io.Reader) ([]*session.Session, error) {
	var out []*session.Session
	skipped, recognized, err := stream(r, func(s *session.Session) { out = append(out, s) })
	return finish(out, skipped, recognized, err)
}

// ParseZip reads the conversations files of an export zip without extracting
// anything. Every other entry is ignored.
func ParseZip(r io.ReaderAt, size int64) ([]*session.Session, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("not a readable zip file: %w", err)
	}
	var files []*zip.File
	for _, f := range zr.File {
		if !f.FileInfo().IsDir() && convFile.MatchString(path.Base(strings.ReplaceAll(f.Name, `\`, "/"))) {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return nil, ErrUnsupported
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	var out []*session.Session
	skipped, recognized := 0, 0
	for _, f := range files {
		if f.UncompressedSize64 > maxEntry {
			return out, fmt.Errorf("%s: larger than %d GiB, refusing to read it", f.Name, maxEntry>>30)
		}
		rc, err := f.Open()
		if err != nil {
			return out, fmt.Errorf("%s: %w", f.Name, err)
		}
		s, rec, err := stream(&capReader{r: rc}, func(s *session.Session) { out = append(out, s) })
		rc.Close()
		skipped += s
		recognized += rec
		if err != nil {
			return finish(out, skipped, recognized, fmt.Errorf("%s: %w", f.Name, err))
		}
	}
	return finish(out, skipped, recognized, nil)
}

// conversations.json, or conversations-000.json and so on.
var convFile = regexp.MustCompile(`^conversations(-\d+)?\.json$`)

func finish(out []*session.Session, skipped, recognized int, err error) ([]*session.Session, error) {
	if err != nil {
		return out, err
	}
	if skipped > 0 {
		return out, &SkippedError{Skipped: skipped}
	}
	return out, nil
}

// capReader fails once more than maxEntry bytes were read.
type capReader struct {
	r io.Reader
	n int64
}

func (c *capReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.n > maxEntry {
		return n, fmt.Errorf("more than %d GiB after decompression, refusing to read it", maxEntry>>30)
	}
	return n, err
}

// conv holds the fields of both formats; which are present tells them apart.
type conv struct {
	// ChatGPT
	ID             string              `json:"id"`
	ConversationID string              `json:"conversation_id"`
	Title          string              `json:"title"`
	CurrentNode    string              `json:"current_node"`
	Mapping        map[string]*gptNode `json:"mapping"`
	// claude.ai
	UUID         string          `json:"uuid"`
	Name         string          `json:"name"`
	ChatMessages []claudeMessage `json:"chat_messages"`
}

// stream decodes the top-level array one element at a time. recognized counts
// elements that look like a conversation of either format.
func stream(r io.Reader, emit func(*session.Session)) (skipped, recognized int, err error) {
	dec := json.NewDecoder(r)
	tok, err := dec.Token()
	if err != nil {
		return 0, 0, fmt.Errorf("%w (%v)", ErrUnsupported, err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return 0, 0, ErrUnsupported
	}
	seen := 0
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return skipped, recognized, fmt.Errorf("invalid JSON after %d conversation(s): %w", seen, err)
		}
		seen++
		var probe struct {
			Mapping      json.RawMessage `json:"mapping"`
			ChatMessages json.RawMessage `json:"chat_messages"`
		}
		if json.Unmarshal(raw, &probe) != nil || (probe.Mapping == nil && probe.ChatMessages == nil) {
			// An element that is neither format: not counted as a conversation.
			continue
		}
		recognized++
		var c conv
		if err := json.Unmarshal(raw, &c); err != nil {
			skipped++
			continue
		}
		var s *session.Session
		if probe.Mapping != nil {
			s = fromChatGPT(&c)
		} else {
			s = fromClaude(&c)
		}
		if s != nil && len(s.Messages) > 0 {
			emit(s)
		}
	}
	if seen > 0 && recognized == 0 {
		return 0, 0, ErrUnsupported
	}
	return skipped, recognized, nil
}

// --- helpers shared by both formats ---

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(l)
}

func toolLine(name, detail string) string {
	line := "> `" + name + "`"
	if d := firstLine(detail); d != "" {
		line += " " + d
	}
	return line
}

// build merges consecutive same-role messages and fills in the title.
func build(s *session.Session, title string, msgs []session.Message) *session.Session {
	for _, m := range msgs {
		m.Text = strings.TrimSpace(m.Text)
		if m.Text == "" {
			continue
		}
		if n := len(s.Messages); n > 0 && s.Messages[n-1].Role == m.Role {
			s.Messages[n-1].Text += "\n\n" + m.Text
			continue
		}
		s.Messages = append(s.Messages, m)
	}
	s.Title = strings.TrimSpace(title)
	if s.Title == "" {
		for _, m := range s.Messages {
			if m.Role == "user" {
				s.Title = firstLine(m.Text)
				break
			}
		}
	}
	return s
}
