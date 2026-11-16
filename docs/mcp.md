# Let AI tools read the vault (MCP)

[Back to the README](../README.md)

`cortex mcp` is a [Model Context Protocol](https://modelcontextprotocol.io/)
server over the vault, so that Claude Code, Claude Desktop, Cursor, VS Code,
Codex CLI, OpenCode and other MCP clients can look up your past conversations.
It speaks newline-delimited JSON-RPC on stdin and stdout (logs go to stderr),
reads the notes straight from the vault folder, never writes anything, and does
not need `cortex run` to be running. The AI tool starts it when it needs it.

```
cortex mcp [-vault DIR] [-config FILE]
```

The vault is the `-vault` folder, else the one in the config file, else
`~/Cortex`. If it does not exist yet, the tools say that Cortex has not saved
anything; the folder is not created. It understands the protocol revisions
2024-11-05, 2025-03-26, 2025-06-18 and 2025-11-25 (with the `initialize`
handshake) and answers `server/discover` for 2026-07-28.

Three tools, all read-only:

- `search`: `query` (all words must appear, case-insensitive, in the title or
  the text), and optionally `project`, `source`, `since` (`YYYY-MM-DD`, the day
  the conversation started) and `limit` (default 10, at most 50). Returns the
  best matches first (most occurrences, then most recent), each with its path
  in the vault, title, project, source, start time and up to three short
  snippets.
- `read`: `note` (a path returned by `search` or `recent`), and optionally
  `offset` and `max_chars` (default 20000, at most 100000; both count
  characters). A long note comes back in parts and the result says which
  `offset` continues it. Only `.md` files inside the vault can be read: absolute
  paths, `..` and symlinks that lead outside are refused.
- `recent`: the most recently changed notes, optionally for one `project` or
  `source`, to answer "what was I working on?".

Only notes written by Cortex (those with a `session` line in the frontmatter)
are searched; `.trash`, `.obsidian` and `.git` folders are skipped, and
symlinks are not followed. Every value printed from a note (title, project,
source, start, snippets) is shown on one line, with control characters and line
separators replaced by spaces; a note whose file name contains such characters
is left out of `search` and `recent`, since `read` could not be given its path
as printed.

Notes can quote text written by third parties, so every `search`, `recent` and `read`
result starts with a fixed line saying that what follows is a record of past
conversations, to be treated as data and not as instructions, and `read` wraps
the note between two delimiter lines that carry a short hash of the content.
The server's `instructions` say the same. This lowers the risk of prompt
injection through an old conversation; it does not remove it.

## Connecting a tool

The control panel has a "Connect your AI tools" section with, for each tool,
what to do, using the path of the Cortex program that is running:

- Claude Desktop and Cursor: buttons "Add to Claude Desktop" and "Add to
  Cursor", shown when the tool's config folder exists. They add only the
  `mcpServers.cortex` entry to the config file, keep everything else in it as it
  is (also a UTF-8 BOM and the file's permissions), and save the file as `.bak`
  next to it the first time Cortex is added (updating an existing entry later
  leaves the `.bak` alone, so it stays your original). They refuse, telling you
  to paste the snippet by hand, if the file is not a JSON object, has anything
  after the object, or is read-only. Restart the tool
  afterwards. The same JSON is shown to paste by hand
  (`%APPDATA%\Claude\claude_desktop_config.json` or
  `~/Library/Application Support/Claude/claude_desktop_config.json` for Claude
  Desktop, `~/.cursor/mcp.json` for Cursor).
- VS Code: the `mcp.json` form, with `servers`.
- Claude Code: `claude mcp add --scope user cortex -- '/path/to/cortex' mcp`,
  quoted for the shell the panel names: single quotes in bash and zsh (so `$`
  and `!` are inert) and in PowerShell on Windows (where double quotes would
  expand `$` and backticks). On Windows with WSL, the panel also shows the
  command to run inside WSL (bash), which starts the Windows program through
  `/mnt/c/...` (it assumes your drives are mounted under `/mnt`, the WSL
  default); it finds its own config, so it needs no `-vault`.
- Codex CLI: a `[mcp_servers.cortex]` table for `~/.codex/config.toml`.
- OpenCode: `{"mcp": {"servers": {"cortex": {"type": "local", "command": [...]}}}}`
  for `opencode.json`, the shape in the published config schema
  (`https://opencode.ai/config.json`, as read on 2026-11-14, where names placed
  directly under `mcp` are marked deprecated); it has not been tried against
  OpenCode itself. The panel also shows the older
  shape, `{"mcp": {"cortex": {"type": "local", "command": [...], "enabled": true}}}`, for releases that
  do not know `servers`.
- A paragraph to paste into `CLAUDE.md`, `AGENTS.md` or your tool's custom
  instructions, so that agents know the notes exist and treat them as data.

The command is the program's own path, so if you move or rebuild Cortex
elsewhere, use the button again (it says "Update") or paste the new path.

