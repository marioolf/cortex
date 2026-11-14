# Cortex

Cortex saves your conversations with AI tools as Markdown notes in a folder (the
vault). The context you build up with different tools ends up scattered across
their private storage; Cortex gathers it in one place you can read yourself,
open with Obsidian or any Markdown reader, and that any AI agent can look up.
It runs locally and nothing leaves your machine.

## Status

Early development. Cortex captures conversations from the sources below, either
once with `cortex sync` or continuously in the background with `cortex run`,
which also serves a local control panel.

| Source | Checked against |
|---|---|
| Claude Code (CLI and desktop) | real sessions |
| GitHub Copilot Chat in VS Code | the VS Code source and real session files without messages |
| Codex CLI | its source code only |
| OpenCode | its source code only |
| Cursor | community descriptions of its storage only |

The last four have only been tested with made-up samples. Expect rough edges,
and please report sessions that come out wrong. Codex sessions that Codex has
compressed (`.jsonl.zst`) are not read yet.

Planned, none of it implemented yet:

- Web chats (ChatGPT, claude.ai, Gemini) through export import and a browser
  extension.

## Try it

You need Go, at the version given in `go.mod`.

```
go build -o cortex ./cmd/cortex
./cortex
```

On Windows, build with `-ldflags "-H windowsgui"` so that starting Cortex at
login or by double click opens no console window:

```
go build -ldflags "-H windowsgui" -o cortex.exe ./cmd/cortex
```

Cortex then runs without a window and is stopped from the panel's "Quit Cortex"
button. A program built this way does not make PowerShell or an interactive `cmd`
prompt wait: `cortex.exe sync` typed there can return to the prompt at once,
print its summary a moment later, and leave no exit status behind. To wait for
it (in a script, say), use `Start-Process -Wait -NoNewWindow -PassThru .\cortex.exe -ArgumentList 'sync'`
in PowerShell (the result's `ExitCode` is the status), or `start /wait cortex.exe sync` in `cmd`. `run` writes
a log to `cortex.log` next to the config file (see Configuration; it is started
over when it passes 1 MB).

Running `cortex` with no arguments is the same as `cortex run`. It saves every
conversation once, then checks again every 30 seconds, and opens the control
panel in your browser. The panel is served at `http://127.0.0.1:47821/` and
only accepts connections from your own machine. Options for `run`:

- `-no-browser`: do not open the panel.
- `-port N`: use another port.
- `-no-autostart`: see below.
- `-config FILE`: use another config file.

If Cortex is already running, starting it again just opens the panel. If the
port is held by some other program, `run` says so and exits with an error;
pass `-port` to use another one.

The panel shows the last sync and any warnings, and lets you:

- pause and resume capturing;
- change the vault folder, or open it in your file manager;
- turn sources on and off;
- turn projects on and off (conversations outside any project are always
  saved);
- choose whether Cortex starts when you log in;
- connect your AI tools to the vault (see "Let AI tools read the vault");
- quit Cortex.

Turning something off stops capturing it. Notes already written are never
deleted.

The first time `run` starts (when there is no config file yet), it writes the
default config and registers itself to start at login with `run -no-browser`:
the registry's `Run` key on Windows, a launch agent in `~/Library/LaunchAgents`
on macOS, an XDG autostart entry in `~/.config/autostart` on Linux. Pass
`-no-autostart` to skip that, or turn it off in the panel at any time.

When Cortex runs on Windows, it also reads the sessions that Linux-side tools
keep in the home directories of WSL distros that are running at that moment. It does not
start distros that are stopped.

`cortex sync` does one round and exits, writing one note per session. It exits
with status 1 if any note could not be written. The default vault is
`~/Cortex`; use `-vault DIR` to choose another folder for that run without
saving it. Running it again only updates what changed. A paused Cortex does not
sync.

## Let AI tools read the vault

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
symlinks are not followed.

Notes can quote text written by third parties, so every `search` and `read`
result starts with a fixed line saying that what follows is a record of past
conversations, to be treated as data and not as instructions, and `read` wraps
the note between two delimiter lines that carry a short hash of the content.
The server's `instructions` say the same. This lowers the risk of prompt
injection through an old conversation; it does not remove it.

### Connecting a tool

The control panel has a "Connect your AI tools" section with, for each tool,
what to do, using the path of the Cortex program that is running:

- Claude Desktop and Cursor: buttons "Add to Claude Desktop" and "Add to
  Cursor", shown when the tool's config folder exists. They add only the
  `mcpServers.cortex` entry to the config file, keep everything else in it as it
  is, save the old file as `.bak` next to it, and refuse (telling you to paste
  the snippet by hand) if the file is not a JSON object. Restart the tool
  afterwards. The same JSON is shown to paste by hand
  (`%APPDATA%\Claude\claude_desktop_config.json` or
  `~/Library/Application Support/Claude/claude_desktop_config.json` for Claude
  Desktop, `~/.cursor/mcp.json` for Cursor).
- VS Code: the `mcp.json` form, with `servers`.
- Claude Code: `claude mcp add --scope user cortex -- "/path/to/cortex" mcp`.
  On Windows with WSL, the panel also shows the command to run inside WSL, which
  starts the Windows program through `/mnt/c/...`; it finds its own config, so it
  needs no `-vault`.
- Codex CLI: a `[mcp_servers.cortex]` table for `~/.codex/config.toml`.
- OpenCode: an `mcp` entry for `opencode.json`.
- A paragraph to paste into `CLAUDE.md`, `AGENTS.md` or your tool's custom
  instructions, so that agents know the notes exist and treat them as data.

The command is the program's own path, so if you move or rebuild Cortex
elsewhere, use the button again (it says "Update") or paste the new path.

## What you get

```
Cortex/
  README.md
  Projects/<project>/   sessions started inside a project directory
  Chats/<source>/       everything else
```

A project is named after the git repository root of the session's directory if
there is one, otherwise after the directory itself. Each note looks like this:

```markdown
---
source: "claude-code"
session: "0b6c1f2e-..."
project: "myapp"
cwd: "/home/me/code/myapp"
started: 2026-05-01T09:30:00Z
generated: true
---

# Fix the login redirect

<!-- cortex:start -->
## User · 2026-05-01 11:30

Why does the login page redirect twice?

## Assistant · 2026-05-01 11:30

...
<!-- cortex:end -->
```

The rules:

- Cortex only rewrites what is between the markers. Text you add outside them
  is kept.
- You can rename or move notes, also into hidden folders such as `.archive`.
  Cortex finds them again through the `session` line in the frontmatter.
- If you delete a note, Cortex writes it again the next time its conversation
  changes or Cortex restarts, with one exception: if Obsidian is set to move
  deleted files to its own `.trash` folder inside the vault (a setting under
  "Files and links", "Deleted files"), Cortex treats the note in `.trash` as
  that session's note, keeps updating it there and does not write a new one,
  until the trash is emptied. To stop capturing something for good, turn its
  project or source off in the panel, or remove the markers from the note.
- If you remove the markers, Cortex leaves that note alone from then on.
- Tool calls are kept as one line each. Tool output and model thinking are not
  saved.

## Privacy

Everything stays on your machine. Before a note is written, Cortex redacts
secrets and replaces them with `[REDACTED]`. It covers:

- private key blocks;
- API keys and tokens with well-known prefixes (OpenAI-style `sk-`, GitHub,
  GitLab, AWS access key IDs, Slack, Google API keys);
- JSON Web Tokens;
- `Authorization` header values (Bearer, Basic, token and similar);
- values assigned to names such as password, passphrase, secret, token or
  api_key (`name: value`, `name=value`, `"name": "value"`, also a quoted value
  with spaces), including prefixed forms like `DB_PASSWORD`, `client_secret` or
  `x-api-key`. A name that merely starts with one of those words, such as
  `tokenizer`, is not touched, nor are values that look like a type (`string`,
  `Optional[str]`), a variable or call (`${TOKEN}`, `self.token`, `getToken()`)
  or a placeholder (`<your-token>`);
- `--password VALUE` and `--token VALUE` command-line flags, `.netrc`-style
  `machine ... password VALUE`, `curl -u user:VALUE`, and the password in
  `https://user:VALUE@host`;
- any extra patterns you add in the configuration.

Redaction is best-effort and errs on the side of removing too much, but it will
miss things. Be careful with a synced or shared vault.

Notes can contain text written by third parties (web pages, files, command
output that was quoted in a conversation). An agent reading the vault should
treat it as data, not as instructions.

## Configuration

Optional. Cortex works without it. The file is a JSON document at the OS config
directory:

- Linux: `~/.config/cortex/config.json`
- Windows: `%AppData%\cortex\config.json`
- macOS: `~/Library/Application Support/cortex/config.json`

```json
{
  "vault": "/home/me/notes/Cortex",
  "disabled_sources": ["claude-code"],
  "excluded_projects": ["secret-client"],
  "redact_patterns": ["ACME-[0-9]{8}"],
  "paused": false
}
```

- `vault`: where notes are written (default `~/Cortex`; `sync -vault` overrides
  it for one run).
- `disabled_sources`: sources that are not captured.
- `excluded_projects`: project names whose sessions are not saved.
- `redact_patterns`: extra regular expressions to redact, on top of the
  built-in ones.
- `paused`: capture nothing until set back to `false`.

The panel writes this file when you change a setting, starting from what is in
the file at that moment, so edits made by hand while Cortex is running are kept
(unless the file no longer parses, in which case the panel's in-memory settings
are written over it). They take effect the next time Cortex starts, or at the
next change made in the panel. `-config FILE` on `run` and `sync` uses a
different file. `run` also keeps its log, `cortex.log`, in the same folder.

## License

MIT. See `LICENSE`.
