// Package connect registers Cortex's MCP server in the config files of AI
// tools that read an "mcpServers" JSON object (Claude Desktop, Cursor), and
// builds the command lines and paths shown for the tools that are set up by hand.
package connect

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Client is a tool whose config file Cortex can edit.
type Client struct {
	ID   string
	Name string
}

var Clients = []Client{{"claude-desktop", "Claude Desktop"}, {"cursor", "Cursor"}}

// Paths returns where each client keeps its config file, by client ID. Tests
// pass their own instead.
func Paths() map[string]string {
	p := map[string]string{}
	if dir, err := os.UserConfigDir(); err == nil { // %AppData%, ~/Library/Application Support, ~/.config
		p["claude-desktop"] = filepath.Join(dir, "Claude", "claude_desktop_config.json")
	}
	if home, err := os.UserHomeDir(); err == nil {
		p["cursor"] = filepath.Join(home, ".cursor", "mcp.json")
	}
	return p
}

// Available reports whether the client looks installed: the folder of its
// config file exists. Cortex never creates that folder.
func Available(path string) bool {
	if path == "" {
		return false
	}
	st, err := os.Stat(filepath.Dir(path))
	return err == nil && st.IsDir()
}

// Entry is the value stored under mcpServers.cortex.
type Entry struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// ErrNotObject means the file must be edited by hand: rewriting it could lose
// what the user wrote there.
var ErrNotObject = errors.New("the file exists but is not a JSON object; paste the snippet by hand instead")

type member struct {
	key string
	raw json.RawMessage
}

// parseObject splits a JSON object into its members in file order. The raw
// values are the original bytes, so what we do not touch is written back as is.
func parseObject(data []byte) ([]member, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // a BOM, as some Windows editors add
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, ErrNotObject
	}
	var ms []member
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil, ErrNotObject
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, ErrNotObject
		}
		ms = append(ms, member{k.(string), raw})
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return nil, ErrNotObject
	}
	if _, err := dec.Token(); err == nil {
		return nil, ErrNotObject // trailing data
	}
	return ms, nil
}

func find(ms []member, key string) int {
	for i, m := range ms {
		if m.key == key {
			return i
		}
	}
	return -1
}

// write renders members as an object, one per line, indented by indent.
func write(ms []member, indent string) []byte {
	if len(ms) == 0 {
		return []byte("{}")
	}
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, m := range ms {
		k, _ := json.Marshal(m.key)
		fmt.Fprintf(&b, "%s  %s: %s", indent, k, m.raw)
		if i < len(ms)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(indent + "}")
	return b.Bytes()
}

// Status says how a config file stands: "" (not connected), "connected" (the
// entry is ours and current) or "outdated" (an entry named cortex that runs
// something else, e.g. the executable moved).
func Status(path string, want Entry) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	ms, err := parseObject(data)
	if err != nil {
		return "", err
	}
	i := find(ms, "mcpServers")
	if i < 0 {
		return "", nil
	}
	servers, err := parseObject(ms[i].raw)
	if err != nil {
		return "", err
	}
	j := find(servers, "cortex")
	if j < 0 {
		return "", nil
	}
	var got Entry
	if json.Unmarshal(servers[j].raw, &got) == nil && got.Command == want.Command && strings.Join(got.Args, "\x00") == strings.Join(want.Args, "\x00") {
		return "connected", nil
	}
	return "outdated", nil
}

// Add sets mcpServers.cortex in the config file at path and keeps everything
// else as it was. The original is saved next to it as path+".bak" first, and
// the new content replaces it atomically.
func Add(path string, e Entry) error {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target // a dotfile manager's symlink must stay a symlink
	}
	data, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	ms, err := parseObject(data)
	if err != nil {
		return err
	}
	var servers []member
	if i := find(ms, "mcpServers"); i >= 0 {
		if servers, err = parseObject(ms[i].raw); err != nil {
			return fmt.Errorf("mcpServers is not a JSON object; paste the snippet by hand instead")
		}
	}
	entry, _ := json.MarshalIndent(e, "    ", "  ")
	if j := find(servers, "cortex"); j >= 0 {
		servers[j].raw = entry
	} else {
		servers = append(servers, member{"cortex", entry})
	}
	block := write(servers, "  ")
	if i := find(ms, "mcpServers"); i >= 0 {
		ms[i].raw = block
	} else {
		ms = append(ms, member{"mcpServers", block})
	}
	out := append(write(ms, ""), '\n')

	if existed {
		if err := os.WriteFile(path+".bak", data, 0o600); err != nil {
			return fmt.Errorf("could not save a backup: %w", err)
		}
	}
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cortex-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ClaudeCodeCommand is the line that registers Cortex with Claude Code. goos
// picks the quoting: double quotes are right for cmd and PowerShell, and
// POSIX shells need the characters they treat specially escaped.
func ClaudeCodeCommand(exe, goos string) string {
	return "claude mcp add --scope user cortex -- " + shellQuote(exe, goos) + " mcp"
}

func shellQuote(s, goos string) string {
	if goos != "windows" {
		s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(s)
	}
	return `"` + s + `"`
}

// WSLPath turns a Windows drive path into the path WSL sees it under
// (C:\Users\me\cortex.exe becomes /mnt/c/Users/me/cortex.exe). UNC paths and
// anything else have no such form.
func WSLPath(p string) (string, bool) {
	if len(p) < 3 || p[1] != ':' || (p[2] != '\\' && p[2] != '/') || !isLetter(p[0]) {
		return "", false
	}
	return "/mnt/" + strings.ToLower(p[:1]) + "/" + strings.ReplaceAll(p[3:], `\`, "/"), true
}

func isLetter(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }

// Executable is this program's absolute path, as agents should launch it.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(exe)
}
