package importer

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/marioolf/cortex/internal/session"
)

type gptNode struct {
	ID       string      `json:"id"`
	Parent   string      `json:"parent"`
	Children []string    `json:"children"`
	Message  *gptMessage `json:"message"`
}

type gptMessage struct {
	Author struct {
		Role string `json:"role"`
	} `json:"author"`
	CreateTime *float64 `json:"create_time"`
	Content    struct {
		ContentType string            `json:"content_type"`
		Parts       []json.RawMessage `json:"parts"`
		Text        string            `json:"text"`
	} `json:"content"`
	Metadata struct {
		Hidden        bool `json:"is_visually_hidden_from_conversation"`
		UserSystemMsg bool `json:"is_user_system_message"`
	} `json:"metadata"`
	Recipient string `json:"recipient"`
}

func fromChatGPT(c *conv) *session.Session {
	id := c.ID
	if id == "" {
		id = c.ConversationID
	}
	s := &session.Session{Source: SourceChatGPT, ID: id}
	var msgs []session.Message
	for _, n := range currentBranch(c) {
		if m, ok := gptVisible(n.Message); ok {
			msgs = append(msgs, m)
		}
	}
	return build(s, c.Title, msgs)
}

// currentBranch walks from current_node up to the root and reverses, which is
// the conversation as shown: edited or regenerated branches are siblings in
// the tree and must not be flattened into it.
func currentBranch(c *conv) []*gptNode {
	cur := c.CurrentNode
	if _, ok := c.Mapping[cur]; !ok {
		cur = latestLeaf(c.Mapping)
	}
	var rev []*gptNode
	seen := map[string]bool{}
	for cur != "" && !seen[cur] {
		n, ok := c.Mapping[cur]
		if !ok || n == nil {
			break
		}
		seen[cur] = true
		rev = append(rev, n)
		cur = n.Parent
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// latestLeaf is the fallback when current_node is missing: the childless node
// with the newest message.
func latestLeaf(m map[string]*gptNode) string {
	best, bestT := "", -1.0
	for id, n := range m {
		if n == nil || len(n.Children) > 0 {
			continue
		}
		t := 0.0
		if n.Message != nil && n.Message.CreateTime != nil {
			t = *n.Message.CreateTime
		}
		if t > bestT || (t == bestT && id < best) {
			best, bestT = id, t
		}
	}
	return best
}

func gptVisible(m *gptMessage) (session.Message, bool) {
	none := session.Message{}
	if m == nil || m.Metadata.Hidden || m.Metadata.UserSystemMsg {
		return none, false
	}
	var t time.Time
	if m.CreateTime != nil && *m.CreateTime > 0 {
		sec := int64(*m.CreateTime)
		t = time.Unix(sec, int64((*m.CreateTime-float64(sec))*1e9)).UTC()
	}
	ct := m.Content.ContentType
	switch m.Author.Role {
	case "user":
		if ct != "text" && ct != "multimodal_text" {
			return none, false // user_editable_context and friends
		}
		text := cleanCitations(partsText(m.Content.Parts))
		return session.Message{Role: "user", Text: text, Time: t}, strings.TrimSpace(text) != ""
	case "assistant":
		if m.Recipient != "" && m.Recipient != "all" {
			line, ok := gptToolLine(m)
			return session.Message{Role: "assistant", Text: line, Time: t}, ok
		}
		if ct != "text" && ct != "multimodal_text" {
			return none, false // thoughts, reasoning_recap, model_editable_context, code...
		}
		text := cleanCitations(partsText(m.Content.Parts))
		return session.Message{Role: "assistant", Text: text, Time: t}, strings.TrimSpace(text) != ""
	}
	return none, false // system, tool
}

// partsText keeps the string parts, and the transcript of spoken input;
// image and file pointers (objects) are dropped.
func partsText(parts []json.RawMessage) string {
	var out []string
	for _, p := range parts {
		var s string
		if json.Unmarshal(p, &s) == nil {
			if strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
			continue
		}
		var o struct {
			ContentType string `json:"content_type"`
			Text        string `json:"text"`
		}
		if json.Unmarshal(p, &o) == nil && o.ContentType == "audio_transcription" && strings.TrimSpace(o.Text) != "" {
			out = append(out, o.Text)
		}
	}
	return strings.Join(out, "\n\n")
}

func gptToolLine(m *gptMessage) (string, bool) {
	text := m.Content.Text
	if text == "" {
		text = partsText(m.Content.Parts)
	}
	switch m.Recipient {
	case "bio": // memory writes, not conversation
		return "", false
	case "web", "web.run", "browser", "browser.search":
		return toolLine("web", webQuery(text)), true
	}
	return toolLine(m.Recipient, text), true
}

// webQuery pulls the search from a web.run call ({"search_query":[{"q":...}]}
// or {"query": ...}); anything else is shown as its first line.
func webQuery(text string) string {
	var j struct {
		Search []struct {
			Q string `json:"q"`
		} `json:"search_query"`
		Query string `json:"query"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(text)), &j) == nil {
		if len(j.Search) > 0 && j.Search[0].Q != "" {
			return j.Search[0].Q
		}
		if j.Query != "" {
			return j.Query
		}
	}
	return text
}

var (
	// U+E200 name U+E202 payload U+E201: cite, filecite, navlist, entity...
	citeMarker    = regexp.MustCompile("([^-]*)?([^]*)")
	privateUse    = regexp.MustCompile("[-]")
	oldCitation   = regexp.MustCompile(`【[^】]*†[^】]*】`)
	entityPayload = regexp.MustCompile(`^\["[^"]*","((?:[^"\\]|\\.)*)"`)
)

// cleanCitations removes ChatGPT's citation markers. An entity marker
// (entity, ["city","Paris",...]) keeps its display name, which is part of the
// sentence; every other marker is dropped with its payload.
func cleanCitations(s string) string {
	if !strings.ContainsAny(s, "") && !strings.Contains(s, "【") && !privateUse.MatchString(s) {
		return s
	}
	s = citeMarker.ReplaceAllStringFunc(s, func(m string) string {
		sub := citeMarker.FindStringSubmatch(m)
		if sub[1] == "entity" {
			if e := entityPayload.FindStringSubmatch(sub[2]); e != nil {
				var name string
				if json.Unmarshal([]byte(`"`+e[1]+`"`), &name) == nil {
					return name
				}
			}
		}
		return ""
	})
	s = oldCitation.ReplaceAllString(s, "")
	return privateUse.ReplaceAllString(s, "")
}
