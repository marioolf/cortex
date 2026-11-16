# Source validation

How each source was checked, so that the table in the [README](../README.md#supported-sources) can be kept honest. Add a line here whenever a source is checked against a new version of its tool.

| Date | Source | Tool version | How | Result |
|---|---|---|---|---|
| 2026-11-14 | Claude Code | 2.1.x | `cortex sync` over the maintainer's real sessions (counts and structure only were inspected) | all sessions with messages became notes; idempotent |
| 2026-11-16 | Codex CLI | 0.169.0 | real binary in an isolated home, driven by a local stand-in model over the Responses API: single turn, `exec resume --last`, a shell tool call, a session in a subfolder, a repository with `AGENTS.md`, and a rollout compressed by Codex itself (`--enable local_thread_store_compression`) | no change needed; fixtures in `internal/collector/codex/testdata/` |
| 2026-11-16 | OpenCode | 1.19.5 | real binary in an isolated home, local stand-in model over Chat Completions: single turn, `run --continue`, a `bash` tool call, a session in a subfolder; storage is SQLite (`opencode.db`, `session`/`message`/`part` tables) | fixed: `opencode run "quoted words"` stores the prompt with literal quotes; fixture in `internal/collector/opencode/testdata/` |
| 2026-11-16 | Copilot Chat (VS Code) | VS Code 1.140.0 | structure-only inspection of a real machine: `chatSessions` files, `state.vscdb` index, `agent-host.db`, `github.copilot-chat/session-store.db` | every store was empty (chat panel opened, no message sent); added a reader for `session-store.db`; message format still unverified |

## Not validated yet

- **Copilot Chat with messages**: needs a real conversation in VS Code. Formats that Cortex does not read yet, because no real data was available: the agent host's per-session `agentSessionData/<id>/session.db` and Copilot CLI's session state.
- **Cursor**: needs a Cursor installation with real chats.
- **ChatGPT and Claude exports**: need a real export file.
- **Browser extension**: needs a real browser, signed in, on chatgpt.com, claude.ai and gemini.google.com.

What was not exercised even for validated tools: compaction, reasoning items, sub-agent sessions, image input, and tools other than the shell tool.
