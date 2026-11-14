// Package cursor reads Cursor chats. Cursor is closed source and its storage is
// undocumented; the format here comes from community reverse engineering and
// changes between releases, so everything is parsed leniently.
//
// Cursor is a VS Code fork and keeps its data in the VS Code user directory:
//
//	<User>/globalStorage/state.vscdb            cursorDiskKV: composerData:<id>, bubbleId:<id>:<bubble>
//	<User>/workspaceStorage/<hash>/state.vscdb  ItemTable: which composers belong to the workspace, legacy chats
//	<User>/workspaceStorage/<hash>/workspace.json  the folder the workspace points at
package cursor

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/marioolf/cortex/internal/collector"
	"github.com/marioolf/cortex/internal/session"
	"github.com/marioolf/cortex/internal/sqliteutil"
)

const ID = "cursor"

type Collector struct {
	// Roots are Cursor "User" directories.
	Roots []string

	seen   collector.Seen    // globalStorage state.vscdb (+ -wal)
	wsSeen collector.Seen    // workspace state.vscdb files (+ -wal)
	fp     map[string]string // per-composer fingerprint, so unchanged chats are not re-emitted
	ws     map[string]wsInfo // by workspace state.vscdb path
}

// wsInfo is what a workspace database tells us, cached until it changes.
type wsInfo struct {
	folder    string
	composers []string
}

func New() *Collector {
	home, _ := os.UserHomeDir()
	var user string
	switch runtime.GOOS {
	case "windows":
		if a := os.Getenv("APPDATA"); a != "" {
			user = filepath.Join(a, "Cursor", "User")
		}
	case "darwin":
		if home != "" {
			user = filepath.Join(home, "Library", "Application Support", "Cursor", "User")
		}
	default:
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" && home != "" {
			cfg = filepath.Join(home, ".config")
		}
		if cfg != "" {
			user = filepath.Join(cfg, "Cursor", "User")
		}
	}
	c := &Collector{}
	if user != "" {
		c.Roots = []string{user}
	}
	return c
}

func (c *Collector) ID() string { return ID }

func (c *Collector) Detect() []string {
	var found []string
	for _, r := range c.Roots {
		_, e1 := os.Stat(filepath.Join(r, "globalStorage", "state.vscdb"))
		_, e2 := os.Stat(filepath.Join(r, "workspaceStorage"))
		if e1 == nil || e2 == nil {
			found = append(found, r)
		}
	}
	return found
}

// Reset makes the next Collect report every chat again.
func (c *Collector) Reset() {
	c.seen.Reset()
	c.wsSeen.Reset()
	c.fp = nil
	c.ws = nil
}

func (c *Collector) Collect(fn func(*session.Session), warn func(error)) {
	if c.fp == nil {
		c.fp = map[string]string{}
		c.ws = map[string]wsInfo{}
	}
	for _, root := range c.Detect() {
		// Workspaces first: they say which folder each composer was used in.
		folders := c.collectWorkspaces(root, fn, warn)
		c.collectGlobal(root, folders, fn, warn)
	}
}

// ---- value helpers ----

func parseTime(raw json.RawMessage) time.Time {
	var n float64
	if json.Unmarshal(raw, &n) == nil && n > 0 {
		return time.UnixMilli(int64(n))
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t
		}
		if ms, err := strconv.ParseInt(s, 10, 64); err == nil && ms > 0 {
			return time.UnixMilli(ms)
		}
	}
	return time.Time{}
}

// role maps the bubble type: 1 = user, 2 = assistant (the old aichat format
// used the words instead).
func role(raw json.RawMessage) string {
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		switch n {
		case 1:
			return "user"
		case 2:
			return "assistant"
		}
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		switch strings.ToLower(s) {
		case "user":
			return "user"
		case "ai", "assistant", "bot":
			return "assistant"
		}
	}
	return ""
}

type bubble struct {
	Type          json.RawMessage `json:"type"`
	Text          string          `json:"text"`
	CreatedAt     json.RawMessage `json:"createdAt"`
	WorkspaceURIs []string        `json:"workspaceUris"`
	Tool          struct {
		Name    string          `json:"name"`
		Params  json.RawMessage `json:"params"`
		RawArgs json.RawMessage `json:"rawArgs"`
	} `json:"toolFormerData"`
}

var toolVersion = regexp.MustCompile(`_v\d+$`)

// toolLine is claudecode's one-line tool summary. The arguments live in
// toolFormerData.params (a JSON string) or rawArgs; the key names below are
// the ones seen in the wild for terminal, file and search tools.
func (b *bubble) toolLine() string {
	if b.Tool.Name == "" {
		return ""
	}
	line := "> `" + toolVersion.ReplaceAllString(b.Tool.Name, "") + "`"
	args := argMap(b.Tool.Params)
	if args == nil {
		args = argMap(b.Tool.RawArgs)
	}
	for _, k := range []string{"explanation", "command", "relativeWorkspacePath", "targetFile", "target_file", "filePath", "path", "pattern", "regex", "globPattern", "query", "searchQuery", "url", "directory", "targetDirectory"} {
		if v, ok := args[k].(string); ok && strings.TrimSpace(v) != "" {
			v, _, _ = strings.Cut(strings.TrimSpace(v), "\n")
			return line + " " + v
		}
	}
	return line
}

// argMap accepts an object or a string holding one.
func argMap(raw json.RawMessage) map[string]any {
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		return m
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && json.Unmarshal([]byte(s), &m) == nil {
		return m
	}
	return nil
}

// addBubble appends one bubble to the session. Thinking and tool results are
// not read at all; empty bubbles (thinking-only, UI placeholders) vanish.
func addBubble(mg *session.Merger, b *bubble) {
	r := role(b.Type)
	if r == "" {
		return
	}
	var parts []string
	if t := strings.TrimSpace(b.Text); t != "" {
		parts = append(parts, t)
	}
	if r == "assistant" {
		if l := b.toolLine(); l != "" {
			parts = append(parts, l)
		}
	}
	if len(parts) == 0 {
		return
	}
	text := strings.Join(parts, "\n\n")
	// An agent turn is dozens of bubbles; one message per turn reads better.
	mg.Add(r, text, parseTime(b.CreatedAt))
}

func finish(s *session.Session, name string, created json.RawMessage) {
	s.Title = strings.TrimSpace(name)
	if s.Title == "" {
		for _, m := range s.Messages {
			if m.Role == "user" {
				s.Title, _, _ = strings.Cut(m.Text, "\n")
				break
			}
		}
	}
	if len(s.Messages) > 0 && s.Messages[0].Time.IsZero() {
		s.Messages[0].Time = parseTime(created)
	}
}

// ---- workspace URIs ----

var driveLetter = regexp.MustCompile(`^/[A-Za-z]:(/|$)`)

// uriToPath turns the folder URIs VS Code stores into a plain path:
// file:///c%3A/Users/me/p -> C:\Users\me\p, file:///home/me/p -> /home/me/p,
// vscode-remote://wsl%2Bubuntu/home/me/p -> /home/me/p (the path on the remote
// side). A string that is not a URI is assumed to be a path already.
func uriToPath(u string) string {
	u = strings.TrimSpace(u)
	i := strings.Index(u, "://")
	if i < 0 {
		return u
	}
	scheme, rest := strings.ToLower(u[:i]), u[i+3:]
	host, p := rest, ""
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		host, p = rest[:j], rest[j:]
	}
	if k := strings.IndexAny(p, "?#"); k >= 0 {
		p = p[:k]
	}
	p, err := url.PathUnescape(p)
	if err != nil || p == "" {
		return ""
	}
	switch {
	case driveLetter.MatchString(p):
		return strings.ToUpper(p[1:2]) + strings.ReplaceAll(p[2:], "/", `\`)
	case scheme == "file" && host != "" && host != "localhost":
		return `\\` + host + strings.ReplaceAll(p, "/", `\`)
	}
	return p
}

type wsIdentifier struct {
	URI struct {
		FSPath   string `json:"fsPath"`
		Path     string `json:"path"`
		External string `json:"external"`
	} `json:"uri"`
}

func (w wsIdentifier) path() string {
	for _, v := range []string{w.URI.FSPath, w.URI.External, w.URI.Path} {
		if v != "" {
			return uriToPath(v)
		}
	}
	return ""
}

// ---- workspaceStorage ----

// collectWorkspaces reads every workspace database that changed, emits legacy
// aichat chats, and returns composerId -> folder for all workspaces.
func (c *Collector) collectWorkspaces(root string, fn func(*session.Session), warn func(error)) map[string]string {
	folders := map[string]string{}
	dirs, _ := filepath.Glob(filepath.Join(root, "workspaceStorage", "*"))
	for _, dir := range dirs {
		db := filepath.Join(dir, "state.vscdb")
		changed, err := sqliteutil.Changed(&c.wsSeen, db)
		if err != nil {
			continue // a workspace without a database is just not a chat workspace
		}
		if changed {
			if err := c.readWorkspace(dir, db, fn); err != nil {
				warn(fmt.Errorf("%s: %w", db, err))
				continue
			}
			sqliteutil.MarkSeen(&c.wsSeen, db)
		}
		info := c.ws[db]
		for _, id := range info.composers {
			folders[id] = info.folder
		}
	}
	return folders
}

func itemValue(db *sql.DB, key string) []byte {
	var v []byte
	if db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", key).Scan(&v) != nil {
		return nil
	}
	return v
}

func (c *Collector) readWorkspace(dir, dbPath string, fn func(*session.Session)) error {
	db, closeDB, err := sqliteutil.Open(dbPath)
	if err != nil {
		return err
	}
	defer closeDB()
	tables, err := sqliteutil.Tables(db)
	if err != nil {
		return err
	}
	info := wsInfo{}
	var ws struct {
		Folder    string `json:"folder"`
		Workspace string `json:"workspace"`
	}
	if data, err := os.ReadFile(filepath.Join(dir, "workspace.json")); err == nil && json.Unmarshal(data, &ws) == nil {
		if ws.Folder != "" {
			info.folder = uriToPath(ws.Folder)
		} else if p := uriToPath(ws.Workspace); p != "" { // a .code-workspace file: use its directory
			info.folder = p[:max(strings.LastIndexAny(p, `/\`), 0)]
		}
	}
	if !tables["ItemTable"] {
		c.ws[dbPath] = info
		return nil
	}

	// Composers that belong to this workspace.
	var cd struct {
		All []struct {
			ID string `json:"composerId"`
		} `json:"allComposers"`
	}
	if json.Unmarshal(itemValue(db, "composer.composerData"), &cd) == nil {
		for _, x := range cd.All {
			if x.ID != "" {
				info.composers = append(info.composers, x.ID)
			}
		}
	}
	c.ws[dbPath] = info

	// Legacy chat panel (before composers): tabs holding bubbles in the same db.
	var chat struct {
		Tabs []struct {
			TabID     string         `json:"tabId"`
			ChatTitle string         `json:"chatTitle"`
			Bubbles   []legacyBubble `json:"bubbles"`
		} `json:"tabs"`
	}
	if json.Unmarshal(itemValue(db, "workbench.panel.aichat.view.aichat.chatdata"), &chat) != nil {
		return nil
	}
	for _, tab := range chat.Tabs {
		if tab.TabID == "" {
			continue
		}
		s := &session.Session{Source: ID, ID: tab.TabID, Cwd: info.folder}
		last := ""
		var mg session.Merger
		for _, lb := range tab.Bubbles {
			b := bubble{Type: lb.Type, Text: lb.text(), CreatedAt: lb.CreatedAt}
			addBubble(&mg, &b)
			last = b.Text
		}
		s.Messages = mg.Messages()
		if len(s.Messages) == 0 {
			continue
		}
		finish(s, tab.ChatTitle, nil)
		key := dbPath + "\x00" + tab.TabID
		fp := strconv.Itoa(len(tab.Bubbles)) + ":" + strconv.Itoa(len(last)) + ":" + info.folder
		if c.fp[key] == fp {
			continue
		}
		c.fp[key] = fp
		fn(s)
	}
	return nil
}

// legacyBubble is a bubble of the old chat panel: type is "user" or "ai", and
// the text is in text or rawText depending on the version.
type legacyBubble struct {
	Type      json.RawMessage `json:"type"`
	Text      string          `json:"text"`
	RawText   string          `json:"rawText"`
	Content   string          `json:"content"`
	CreatedAt json.RawMessage `json:"timestamp"`
}

func (l legacyBubble) text() string {
	for _, t := range []string{l.Text, l.RawText, l.Content} {
		if strings.TrimSpace(t) != "" {
			return t
		}
	}
	return ""
}

// ---- globalStorage ----

type header struct {
	BubbleID string `json:"bubbleId"`
}

type composerData struct {
	Name         string          `json:"name"`
	CreatedAt    json.RawMessage `json:"createdAt"`
	Conversation []bubble        `json:"conversation"` // older: bubbles inline
	Headers      []header        `json:"fullConversationHeadersOnly"`
	HeaderLog    struct {
		ChunkCount int `json:"chunkCount"`
	} `json:"headerLog"`
	Workspace wsIdentifier `json:"workspaceIdentifier"`
}

// collectGlobal reads composers whose fingerprint changed. Cursor writes to
// this database all the time and it can reach many GB, so a changed file is
// only a reason to compare fingerprints, not to re-read every chat.
func (c *Collector) collectGlobal(root string, folders map[string]string, fn func(*session.Session), warn func(error)) {
	path := filepath.Join(root, "globalStorage", "state.vscdb")
	changed, err := sqliteutil.Changed(&c.seen, path)
	if err != nil {
		return // no global database yet
	}
	if !changed {
		return
	}
	db, closeDB, err := sqliteutil.Open(path)
	if err != nil {
		warn(fmt.Errorf("%s: %w", path, err))
		return
	}
	defer closeDB()
	tables, err := sqliteutil.Tables(db)
	if err != nil {
		warn(fmt.Errorf("%s: %w", path, err))
		return
	}
	if !tables["cursorDiskKV"] {
		sqliteutil.MarkSeen(&c.seen, path)
		return
	}
	fps, err := fingerprints(db)
	if err != nil {
		warn(fmt.Errorf("%s: %w", path, err))
		return
	}
	headerWS := map[string]string{}
	if tables["ItemTable"] {
		headerWS = composerHeaderFolders(db)
	}

	ids := make([]string, 0, len(fps))
	for id := range fps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	ok := true
	for _, id := range ids {
		key := path + "\x00" + id
		fp := fps[id] + "|" + folders[id] + "|" + headerWS[id]
		if c.fp[key] == fp {
			continue
		}
		s, err := readComposer(db, id, firstNonEmpty(folders[id], headerWS[id]))
		if err != nil {
			warn(fmt.Errorf("%s: composer %s: %w", path, id, err))
			ok = false
			continue
		}
		c.fp[key] = fp
		if s != nil && len(s.Messages) > 0 {
			fn(s)
		}
	}
	if ok {
		sqliteutil.MarkSeen(&c.seen, path)
	}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// fingerprints returns, per composer, something that changes whenever its
// data does, without reading any values. cursorDiskKV declares its key UNIQUE
// ON CONFLICT REPLACE, so rewriting a row gives it a new rowid; the size and
// the bubble count and newest bubble rowid cover writers that update in place
// or add bubbles.
func fingerprints(db *sql.DB) (map[string]string, error) {
	fps := map[string]string{}
	rows, err := db.Query(`SELECT substr(key, 14), rowid, length(CAST(value AS BLOB)) FROM cursorDiskKV WHERE key >= 'composerData:' AND key < 'composerData;'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var rowid, size sql.NullInt64
		if rows.Scan(&id, &rowid, &size) == nil && id != "" {
			fps[id] = fmt.Sprintf("%d:%d", rowid.Int64, size.Int64)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// key is "bubbleId:<composer>:<bubble>"; 'bubbleId:' is 9 characters.
	rows, err = db.Query(`SELECT substr(key, 10, instr(substr(key, 10), ':') - 1) AS c, count(*), max(rowid)
		FROM cursorDiskKV WHERE key >= 'bubbleId:' AND key < 'bubbleId;' GROUP BY c`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n, maxRow sql.NullInt64
		if rows.Scan(&id, &n, &maxRow) != nil {
			continue
		}
		if _, ok := fps[id]; ok { // bubbles without a composerData row are orphans
			fps[id] += fmt.Sprintf("|%d:%d", n.Int64, maxRow.Int64)
		}
	}
	return fps, rows.Err()
}

// composerHeaderFolders reads the global composer index some versions keep in
// ItemTable, which records each composer's workspace.
func composerHeaderFolders(db *sql.DB) map[string]string {
	out := map[string]string{}
	var idx struct {
		All []struct {
			ID        string       `json:"composerId"`
			Workspace wsIdentifier `json:"workspaceIdentifier"`
		} `json:"allComposers"`
	}
	if json.Unmarshal(itemValue(db, "composer.composerHeaders"), &idx) == nil {
		for _, x := range idx.All {
			if p := x.Workspace.path(); x.ID != "" && p != "" {
				out[x.ID] = p
			}
		}
	}
	return out
}

func readComposer(db *sql.DB, id, folder string) (*session.Session, error) {
	var raw []byte
	err := db.QueryRow("SELECT value FROM cursorDiskKV WHERE key = ?", "composerData:"+id).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil // deleted since the fingerprint scan
	}
	if err != nil {
		return nil, err
	}
	var cd composerData
	if err := json.Unmarshal(raw, &cd); err != nil {
		return nil, err
	}
	s := &session.Session{Source: ID, ID: id, Cwd: firstNonEmpty(cd.Workspace.path(), folder)}

	bubbles := cd.Conversation
	if len(bubbles) == 0 {
		var err error
		if bubbles, err = loadBubbles(db, id, &cd); err != nil {
			return nil, err
		}
	}
	var mg session.Merger
	for i := range bubbles {
		if s.Cwd == "" && len(bubbles[i].WorkspaceURIs) > 0 {
			s.Cwd = uriToPath(bubbles[i].WorkspaceURIs[0])
		}
		addBubble(&mg, &bubbles[i])
	}
	s.Messages = mg.Messages()
	finish(s, cd.Name, cd.CreatedAt)
	return s, nil
}

// loadBubbles returns a composer's bubbles in conversation order: the order of
// its header list (inline, or chunked in composerConvHeaders keys in newer
// builds), or creation time when there is no list.
func loadBubbles(db *sql.DB, id string, cd *composerData) ([]bubble, error) {
	headers := cd.Headers
	if len(headers) == 0 && cd.HeaderLog.ChunkCount > 0 {
		rows, err := db.Query("SELECT value FROM cursorDiskKV WHERE key >= ? AND key < ? ORDER BY key",
			"composerConvHeaders:"+id+":", "composerConvHeaders:"+id+";")
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var v []byte
			var chunk []header
			if rows.Scan(&v) == nil && json.Unmarshal(v, &chunk) == nil {
				headers = append(headers, chunk...)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}

	prefix := "bubbleId:" + id + ":"
	rows, err := db.Query("SELECT key, value FROM cursorDiskKV WHERE key >= ? AND key < ?", prefix, "bubbleId:"+id+";")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]bubble{}
	var order []string
	for rows.Next() {
		var key string
		var v []byte
		if rows.Scan(&key, &v) != nil {
			continue
		}
		var b bubble
		if json.Unmarshal(v, &b) != nil {
			continue // one bad bubble must not lose the conversation
		}
		bid := strings.TrimPrefix(key, prefix)
		byID[bid] = b
		order = append(order, bid)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []bubble
	if len(headers) > 0 {
		done := map[string]bool{}
		for _, h := range headers {
			if b, ok := byID[h.BubbleID]; ok && !done[h.BubbleID] {
				done[h.BubbleID] = true
				out = append(out, b)
			}
		}
		return out, nil
	}
	sort.SliceStable(order, func(i, j int) bool {
		return parseTime(byID[order[i]].CreatedAt).Before(parseTime(byID[order[j]].CreatedAt))
	})
	for _, bid := range order {
		out = append(out, byID[bid])
	}
	return out, nil
}
