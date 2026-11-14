package importer

import (
	"strings"
	"time"

	"github.com/marioolf/cortex/internal/session"
)

type claudeMessage struct {
	Sender    string        `json:"sender"`
	Text      string        `json:"text"`
	CreatedAt string        `json:"created_at"`
	Content   []claudeBlock `json:"content"`
}

type claudeBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

func fromClaude(c *conv) *session.Session {
	s := &session.Session{Source: SourceClaudeAI, ID: c.UUID}
	var msgs []session.Message
	for _, m := range c.ChatMessages {
		role := map[string]string{"human": "user", "assistant": "assistant"}[m.Sender]
		if role == "" {
			continue
		}
		var text string
		if len(m.Content) > 0 {
			text = blocksText(m.Content)
		} else {
			text = m.Text
		}
		t, _ := time.Parse(time.RFC3339, m.CreatedAt)
		msgs = append(msgs, session.Message{Role: role, Text: text, Time: t})
	}
	return build(s, c.Name, msgs)
}

// blocksText keeps text and one line per tool call; tool results, thinking
// and anything else are dropped.
func blocksText(blocks []claudeBlock) string {
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				parts = append(parts, t)
			}
		case "tool_use":
			detail := ""
			for _, k := range []string{"description", "query", "url", "title", "path", "file_path", "command", "pattern"} {
				if v, ok := b.Input[k].(string); ok && strings.TrimSpace(v) != "" {
					detail = v
					break
				}
			}
			parts = append(parts, toolLine(b.Name, detail))
		}
	}
	return strings.Join(parts, "\n\n")
}
