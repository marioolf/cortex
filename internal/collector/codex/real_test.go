package codex

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/marioolf/cortex/internal/session"
)

// Fixtures in testdata/ were written by the real Codex CLI 0.169.0
// (`codex exec`, custom Responses-API provider answering canned text from a
// local fake server). Only sandbox paths were replaced by /home/dev/demo and
// long prompt texts were trimmed; the record layout is untouched. Notable
// facts of this release, all handled by Parse:
//   - session_meta carries "history_mode":"paginated" and every line an
//     "ordinal"; there are also "world_state" and "token_usage_record" lines;
//   - a typed message is logged both as response_item/message and as
//     event_msg/item_completed (UserMessage/AgentMessage);
//   - the first user item holds the injected <environment_context> block (and
//     the "# AGENTS.md instructions" block when the repo has an AGENTS.md);
//     it is a separate content part of the same message as the typed text;
//   - the shell tool is function_call "exec_command" with {"cmd": "..."};
//   - tool output is a function_call_output item.

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestRealRollouts(t *testing.T) {
	const (
		single = "rollout-2026-11-16T16-36-57-01a1e501-19dd-7ca3-a6b3-853029f825a2.jsonl"
		multi  = "rollout-2026-11-16T16-37-06-01a1e501-3e8a-7bd0-959b-fa0535b0049d.jsonl"
		subdir = "rollout-2026-11-16T16-37-06-01a1e501-40a7-7d31-9a97-ba867efe620f.jsonl"
		agents = "rollout-2026-11-16T16-40-12-01a1e504-146f-7a32-a874-22e5425cf2a4.jsonl"
	)
	canned := "Canned answer number 2. Hello from the fake model."
	for _, c := range []struct {
		name string
		file string
		want session.Session
	}{
		{"single turn", single, session.Session{
			Source: ID, ID: "01a1e501-19dd-7ca3-a6b3-853029f825a2", Cwd: "/home/dev/demo", Title: "Say hello single turn",
			Messages: []session.Message{
				{Role: "user", Text: "Say hello single turn", Time: at("2026-11-16T15:36:57.159Z")},
				{Role: "assistant", Text: canned, Time: at("2026-11-16T15:36:57.181Z")},
			}}},
		{"tool call, tool result and a resumed second prompt", multi, session.Session{
			Source: ID, ID: "01a1e501-3e8a-7bd0-959b-fa0535b0049d", Cwd: "/home/dev/demo", Title: "Please RUNTOOL and tell me the output",
			Messages: []session.Message{
				{Role: "user", Text: "Please RUNTOOL and tell me the output", Time: at("2026-11-16T15:37:06.537Z")},
				// one tool line, no tool output ("cortex-check" appears only in the final text)
				{Role: "assistant", Text: "I will run a command.\n\n> `exec_command` echo cortex-check\n\nTool done: output was cortex-check. FINAL-ANSWER", Time: at("2026-11-16T15:37:06.556Z")},
				{Role: "user", Text: "Second prompt of the same session", Time: at("2026-11-16T15:37:06.865Z")},
				{Role: "assistant", Text: "Canned answer number 4. Hello from the fake model.", Time: at("2026-11-16T15:37:06.885Z")},
			}}},
		{"started in a subdirectory", subdir, session.Session{
			Source: ID, ID: "01a1e501-40a7-7d31-9a97-ba867efe620f", Cwd: "/home/dev/demo/sub/dir", Title: "Hello from the subdirectory",
			Messages: []session.Message{
				{Role: "user", Text: "Hello from the subdirectory", Time: at("2026-11-16T15:37:07.074Z")},
				{Role: "assistant", Text: canned, Time: at("2026-11-16T15:37:07.094Z")},
			}}},
		{"AGENTS.md block injected before the prompt", agents, session.Session{
			Source: ID, ID: "01a1e504-146f-7a32-a874-22e5425cf2a4", Cwd: "/home/dev/demo", Title: "Hello with AGENTS.md present",
			Messages: []session.Message{
				{Role: "user", Text: "Hello with AGENTS.md present", Time: at("2026-11-16T15:40:12.365Z")},
				{Role: "assistant", Text: canned, Time: at("2026-11-16T15:40:12.384Z")},
			}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseFile(filepath.Join("testdata", c.file))
			if err != nil {
				t.Fatal(err)
			}
			if got.Source != c.want.Source || got.ID != c.want.ID || got.Cwd != c.want.Cwd || got.Title != c.want.Title {
				t.Fatalf("meta: %+v", got)
			}
			if len(got.Messages) != len(c.want.Messages) {
				t.Fatalf("messages: %+v", got.Messages)
			}
			for i, m := range c.want.Messages {
				g := got.Messages[i]
				if g.Role != m.Role || g.Text != m.Text || !g.Time.Equal(m.Time) {
					t.Errorf("message %d:\n got %+v\nwant %+v", i, g, m)
				}
			}
		})
	}
}

// Codex's own compression (feature local_thread_store_compression, still
// "under development" in 0.169.0 and off by default) turned a rollout older
// than 7 days into rollout-*.jsonl.zst and removed the plain file. The fixture
// is the single-turn rollout recompressed at zstd level 3 after sanitising.
func TestRealCompressedRollout(t *testing.T) {
	got, err := ParseFile(filepath.Join("testdata", "zst", "rollout-2026-11-16T16-36-57-01a1e501-19dd-7ca3-a6b3-853029f825a2.jsonl.zst"))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "01a1e501-19dd-7ca3-a6b3-853029f825a2" || got.Cwd != "/home/dev/demo" || len(got.Messages) != 2 ||
		got.Messages[0].Text != "Say hello single turn" || got.Messages[1].Role != "assistant" {
		t.Fatalf("got %+v", got)
	}
}
