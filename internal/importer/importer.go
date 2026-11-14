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

// Default caps. The element cap bounds memory (one conversation at a time is
// held, plus working copies); the entry and total caps bound the time spent on
// a zip bomb. They are far above any real export: the biggest known ones are a
// few hundred MB in total, and a single conversation of 64 MiB would be a book.
const (
	defaultMaxElement = 64 << 20
	defaultMaxEntry   = 2 << 30
	defaultMaxTotal   = 8 << 30
)

// ErrUnsupported means the file is not a ChatGPT or claude.ai export.
var ErrUnsupported = errors.New("unsupported file: expected a ChatGPT export (zip or conversations.json) or a claude.ai export (zip or conversations.json)")

// Options changes the caps of Each; zero values mean the defaults. Tests lower
// them.
type Options struct {
	MaxElement int64 // largest raw JSON of one conversation; larger ones are skipped
	MaxEntry   int64 // largest decompressed size of one file
	MaxTotal   int64 // most bytes read from all the files of one export
	// Skip is told why each unreadable conversation was skipped. May be nil.
	Skip func(reason string)
	// Clean is applied to a conversation title before it is put in a Skip
	// reason (the engine redacts it there). May be nil.
	Clean func(string) string
}

func (o Options) withDefaults() Options {
	if o.MaxElement <= 0 {
		o.MaxElement = defaultMaxElement
	}
	if o.MaxEntry <= 0 {
		o.MaxEntry = defaultMaxEntry
	}
	if o.MaxTotal <= 0 {
		o.MaxTotal = defaultMaxTotal
	}
	return o
}

// Each reads an export and calls fn for every conversation, one at a time: no
// more than one conversation is ever held in memory. path may be the zip as
// downloaded or one of the JSON files inside it; the kind is detected from the
// content, not the name. unreadable counts the conversations that were skipped
// (not decodable, too large, without an id, or an id already seen in this
// export; the first one wins). An error from fn stops the import and is
// returned as is. Any other error (unsupported file, broken zip, truncated
// JSON, a cap exceeded) also stops it; the conversations before it were
// delivered.
func Each(path string, fn func(*session.Session) error) (unreadable int, err error) {
	return EachWith(path, Options{}, fn)
}

// EachWith is Each with other caps.
func EachWith(path string, opt Options, fn func(*session.Session) error) (unreadable int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	var magic [4]byte
	n, _ := io.ReadFull(f, magic[:])
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	r := &run{opt: opt.withDefaults(), fn: fn, ids: map[string]struct{}{}}
	if bytes.Equal(magic[:n], []byte("PK\x03\x04")) {
		err = r.zip(f, st.Size())
	} else {
		err = r.file("", f)
	}
	return r.unreadable, err
}

// run is the state of one Each call.
type run struct {
	opt        Options
	fn         func(*session.Session) error
	unreadable int
	inZip      bool                // the data comes from a zip entry
	fnErr      error               // set when the callback failed
	total      int64               // bytes read so far from all files
	ids        map[string]struct{} // source + id of the conversations delivered
}

// conversations.json, or conversations-000.json and so on.
var convFile = regexp.MustCompile(`^conversations(-\d+)?\.json$`)

// zip reads the conversations files of an export zip without extracting
// anything. Every other entry is ignored.
func (r *run) zip(ra io.ReaderAt, size int64) error {
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return fmt.Errorf("damaged zip file (it cannot be read as a zip): %w", err)
	}
	r.inZip = true
	var files []*zip.File
	for _, f := range zr.File {
		if !f.FileInfo().IsDir() && convFile.MatchString(path.Base(strings.ReplaceAll(f.Name, `\`, "/"))) {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return ErrUnsupported
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	for _, f := range files {
		if f.UncompressedSize64 > uint64(r.opt.MaxEntry) {
			return fmt.Errorf("%s: larger than %s, refusing to read it", f.Name, size64(r.opt.MaxEntry))
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
		err = r.file(f.Name, rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// file streams one JSON file; name is used in errors, "" for a bare file.
func (r *run) file(name string, rd io.Reader) error {
	err := r.stream(&capReader{r: rd, run: r})
	if err != nil && r.fnErr == nil && name != "" && !errors.Is(err, ErrUnsupported) {
		return fmt.Errorf("%s: %w", name, err)
	}
	return err
}

func size64(n int64) string {
	if n >= 1<<30 && n%(1<<30) == 0 {
		return fmt.Sprintf("%d GiB", n>>30)
	}
	return fmt.Sprintf("%d MiB", n>>20)
}

// errCap marks a size cap that was exceeded.
var errCap = errors.New("size cap exceeded")

// capReader fails once the entry or the whole export is bigger than allowed.
type capReader struct {
	r io.Reader
	n int64
	*run
}

func (c *capReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	c.run.total += int64(n)
	switch {
	case c.n > c.opt.MaxEntry:
		return n, fmt.Errorf("%w: more than %s after decompression, refusing to read it", errCap, size64(c.opt.MaxEntry))
	case c.run.total > c.opt.MaxTotal:
		return n, fmt.Errorf("%w: more than %s read from this export, refusing to go on", errCap, size64(c.opt.MaxTotal))
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

// skip counts one unreadable conversation.
func (r *run) skip(reason string) {
	r.unreadable++
	if r.opt.Skip != nil {
		r.opt.Skip(reason)
	}
}

// stream decodes the top-level array one element at a time.
func (r *run) stream(rd io.Reader) error {
	sc := newScanner(rd)
	if err := sc.start(r.opt.MaxElement); err != nil {
		if errors.Is(err, errCap) || errors.Is(err, errTooMuchSpace) {
			return err
		}
		if errors.Is(err, errNotArray) || err == io.EOF {
			return fmt.Errorf("%w (%v)", ErrUnsupported, err)
		}
		// Not about what the file holds but about reading it: a corrupt entry.
		if r.inZip {
			return fmt.Errorf("damaged zip file (an entry cannot be read): %w", err)
		}
		return fmt.Errorf("the file cannot be read: %w", err)
	}
	seen, recognized := 0, 0 // recognized: elements that look like a conversation of either format
	for first := true; ; first = false {
		raw, size, tooBig, done, err := sc.next(first, r.opt.MaxElement)
		switch {
		case err == nil:
		case errors.Is(err, errCap):
			return fmt.Errorf("after %d conversation(s): %w", seen, err)
		case errors.Is(err, errTooMuchSpace):
			return fmt.Errorf("after %d conversation(s): more than %s of whitespace between elements, refusing to read on", seen, size64(r.opt.MaxElement))
		default:
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return fmt.Errorf("invalid JSON after %d conversation(s): %w", seen, err)
		}
		if done {
			break
		}
		seen++
		if tooBig {
			recognized++
			r.skip(fmt.Sprintf("conversation %d skipped: larger than %s (%d MiB)", seen, size64(r.opt.MaxElement), size>>20))
			continue
		}
		var probe struct {
			Mapping      json.RawMessage `json:"mapping"`
			ChatMessages json.RawMessage `json:"chat_messages"`
		}
		if json.Unmarshal(raw, &probe) != nil || (probe.Mapping == nil && probe.ChatMessages == nil) {
			// An element that is neither format: not counted as a conversation.
			if !json.Valid(raw) {
				recognized++
				r.skip(fmt.Sprintf("conversation %d skipped: not valid JSON", seen))
			}
			continue
		}
		recognized++
		var c conv
		if err := json.Unmarshal(raw, &c); err != nil {
			r.skip(fmt.Sprintf("conversation %d skipped: %v", seen, err))
			continue
		}
		gpt := probe.Mapping != nil
		raw, probe.Mapping, probe.ChatMessages = nil, nil, nil
		var s *session.Session
		if gpt {
			s = fromChatGPT(&c)
		} else {
			s = fromClaude(&c)
		}
		if s == nil || len(s.Messages) == 0 {
			continue
		}
		if strings.TrimSpace(s.ID) == "" {
			r.skip(fmt.Sprintf("conversation %d (%q) skipped: it has no id", seen, r.shownTitle(s.Title)))
			continue
		}
		key := s.Source + "\x00" + s.ID
		if _, dup := r.ids[key]; dup {
			r.skip(fmt.Sprintf("conversation %d skipped: its id %q already appeared earlier in this file (the first one is kept)", seen, s.ID))
			continue
		}
		r.ids[key] = struct{}{}
		if err := r.fn(s); err != nil {
			r.fnErr = err
			return err
		}
	}
	if seen > 0 && recognized == 0 {
		return ErrUnsupported
	}
	// The conversations above were valid and stay delivered; the rest of the
	// file is not something we understand, so the import is incomplete.
	if err := sc.finish(r.opt.MaxElement); err != nil {
		if errors.Is(err, errTooMuchSpace) {
			err = fmt.Errorf("more than %s of whitespace after the end of the export", size64(r.opt.MaxElement))
		}
		return fmt.Errorf("after %d conversation(s): %w", seen, err)
	}
	return nil
}

// maxShownTitle is the most characters of a title a skip message shows.
const maxShownTitle = 80

// shownTitle is the first line of a conversation title as it may appear in a
// message: redacted (when the caller gave a Clean) and cut to maxShownTitle
// characters. The cut after cleaning keeps a half-cut secret out of the text;
// the earlier, wide one only bounds the work of the redactor.
func (r *run) shownTitle(title string) string {
	t := session.CapText(firstLine(title), 2000)
	if r.opt.Clean != nil {
		t = r.opt.Clean(t)
	}
	if rs := []rune(t); len(rs) > maxShownTitle {
		t = string(rs[:maxShownTitle]) + "…"
	}
	return t
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
	var mg session.Merger
	for _, m := range msgs {
		if text := strings.TrimSpace(m.Text); text != "" {
			mg.Add(m.Role, text, m.Time)
		}
	}
	s.Messages = append(s.Messages, mg.Messages()...)
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
