<div align="center">

<img src="docs/assets/logo-256.png" alt="Cortex logo" width="128" height="128">

# Cortex

**One local memory for all your AI tools.**<br>
Cortex saves every conversation you have with AI tools as Markdown notes in a single folder, and lets any AI agent search that folder.

[![CI](https://github.com/marioolf/cortex/actions/workflows/ci.yml/badge.svg)](https://github.com/marioolf/cortex/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/github/go-mod/go-version/marioolf/cortex)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
![Platforms: Windows | macOS | Linux](https://img.shields.io/badge/platforms-Windows%20%7C%20macOS%20%7C%20Linux-lightgrey)
![MCP server](https://img.shields.io/badge/MCP-server-5B2BD9)
![local-first](https://img.shields.io/badge/local--first-no%20cloud-0FB5A6)
[![Release](https://img.shields.io/github/v/release/marioolf/cortex)](https://github.com/marioolf/cortex/releases)

[Install](#install) ·
[First five minutes](#then) ·
[How it works](#how-it-works) ·
[Connect your AI tools](#connect-your-ai-tools-mcp) ·
[Web chats](#web-chats) ·
[Privacy & security](#privacy--security) ·
[FAQ](#faq)

</div>

---

## Install

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/marioolf/cortex/main/install.ps1 | iex
```

**macOS / Linux**

```sh
curl -fsSL https://raw.githubusercontent.com/marioolf/cortex/main/install.sh | sh
```

No Go or any other dependency is needed. To read the script before running it: `curl -fsSL https://raw.githubusercontent.com/marioolf/cortex/main/install.sh -o install.sh && less install.sh` (on Windows, open the same address with `install.ps1` in your browser). Each script downloads the release for your system, checks its SHA-256 against the release's `checksums.txt`, installs Cortex and starts it. The binaries are **not code-signed** yet. To download and verify by hand instead, or to pin a version, see [docs/install.md](docs/install.md).

## Then

1. **The panel opens** in your browser at <http://127.0.0.1:47821/>. Cortex has started saving your conversations and checks again every 30 seconds. (On Windows it lives in the notification area; click its icon to reopen the panel.)
2. **Open the vault folder** (`~/Cortex` by default, or **Open folder** in the panel) in [Obsidian](https://obsidian.md/) or any Markdown reader.
3. **Connect your AI tools** from the panel's **Connect AI tools** tab, so that they can search the vault. For example, for Claude Code:

   ```sh
   claude mcp add --scope user cortex -- /path/to/cortex mcp
   ```

   The tab shows the exact command, with the right path, for each tool. More in [Connect your AI tools](#connect-your-ai-tools-mcp).

Cortex starts at login (the installers set this up) and never contacts the Internet. To update, run the installer again; to remove it, see [Update / Uninstall](#update--uninstall).

<div align="center">
<img src="docs/assets/panel.png" alt="The Cortex control panel: sources, projects, vault and startup in one window (invented sample data)" width="800">
<br>
<sub>The control panel, with invented sample data.</sub>
</div>

## Why Cortex

The context you build up with AI tools ends up scattered across their private storage: a Claude Code session here, a Copilot chat there, a ChatGPT thread in a browser tab. None of it is searchable together, and none of it is yours to read.

Cortex gathers it in one place you control:

- a **folder of plain Markdown notes** (the vault) that you can read yourself, open with [Obsidian](https://obsidian.md/) or any Markdown reader, sync or back up however you like;
- a **small background program** with a local control panel that keeps the folder up to date;
- an **MCP server**, so that Claude Code, Cursor, VS Code, Codex CLI and other agents can look up what you discussed with any other tool. All your tools share one memory.

Everything runs on your machine. There is no account and no cloud service. Nothing leaves your computer, unless you switch on the optional summaries (off by default, see [Summaries](#summaries)).

## Features

- **Many sources, one vault.** Claude Code, GitHub Copilot Chat in VS Code, Codex CLI, OpenCode and Cursor are read from disk; ChatGPT and Claude (web) from an export file; ChatGPT, Claude and Gemini (web) through a browser extension.
- **Runs in the background.** Saves everything once, then checks again every 30 seconds. Starts at login if you want it to (Windows tray icon included).
- **Local control panel** at `127.0.0.1`: pause and resume, choose the vault, switch sources and projects on and off, import exports, pair the extension, connect your AI tools.
- **MCP server** (`cortex mcp`) with three read-only tools: `search`, `read` and `recent`.
- **Plain, durable notes.** One note per conversation, organised by project. You can rename, move or annotate notes; Cortex only rewrites its own block.
- **Secrets are redacted** before a note is written (private keys, API keys, tokens, passwords, your own patterns).
- **Optional summaries** of finished conversations, written by an AI tool you already have installed.
- **Single binary**, no runtime, no telemetry, no update checks.

## How it works

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/how-it-works-dark.svg">
  <img src="docs/assets/how-it-works.svg" alt="Flow diagram: coding assistants (Claude Code, Copilot Chat in VS Code, Codex CLI, OpenCode, Cursor) and web chats (browser extension, export import) feed Cortex (cortex run), which redacts secrets and writes Markdown notes to the vault. cortex mcp lets AI agents search, read and list recent notes. You can open the vault in Obsidian or any editor." width="900">
</picture>

`cortex run` watches the folders where the tools keep their sessions, writes one note per conversation into the vault, and serves the control panel. The browser extension and imports feed the same vault. `cortex mcp` is started by your AI tool when it needs it; it reads the notes straight from the folder and never writes.

## Supported sources

| Source | How it is read | Checked against |
|---|---|---|
| Claude Code (CLI and desktop) | session files on disk | real sessions |
| GitHub Copilot Chat in VS Code | session files on disk | the VS Code source and real session files without messages |
| Codex CLI | session files on disk, including compressed `.jsonl.zst` sessions | its source code only |
| OpenCode | its local storage | its source code only |
| Cursor | its local storage | community descriptions of its storage only |
| ChatGPT (web) | import of an export | community descriptions of the export format and invented samples only; no real export |
| Claude (web) | import of an export | community descriptions of the export format and invented samples only; no real export |
| ChatGPT, Claude and Gemini (web) | browser extension | invented HTML only; never run against the live sites or in a real browser |

Everything below Claude Code and Copilot has only been tested with made-up samples. Expect rough edges, and please [report sessions that come out wrong](https://github.com/marioolf/cortex/issues/new?template=bug_report.md). Gemini has no export that Cortex reads; use the extension.

## Connect your AI tools (MCP)

`cortex mcp` is a [Model Context Protocol](https://modelcontextprotocol.io/) server over the vault. It speaks JSON-RPC on stdin and stdout, reads the notes straight from the folder, never writes anything and does not need `cortex run` to be running: the AI tool starts it when it needs it.

Three read-only tools:

| Tool | What it does |
|---|---|
| `search` | All words must appear (case-insensitive) in the title or text; optional `project`, `source`, `since`, `limit`. Best matches first, with snippets. |
| `read` | Reads a note returned by `search` or `recent`, in parts if it is long. Only `.md` files inside the vault. |
| `recent` | The most recently changed notes, optionally for one project or source: "what was I working on?" |

The easiest way: open the panel's **Connect your AI tools** section. It shows, for each tool, what to do with the path of the Cortex program that is running, and has one-click **Add to Claude Desktop** and **Add to Cursor** buttons. By hand:

```sh
# Claude Code
claude mcp add --scope user cortex -- '/path/to/cortex' mcp
```

```toml
# Codex CLI, in ~/.codex/config.toml
[mcp_servers.cortex]
command = "/path/to/cortex"
args = ["mcp"]
```

Claude Desktop, Cursor, VS Code and OpenCode have their own snippets, and there is a paragraph to paste into `CLAUDE.md` or `AGENTS.md`: see [docs/mcp.md](docs/mcp.md#connecting-a-tool). Results are marked as data, not instructions, to lower the risk of prompt injection through an old conversation; this does not remove it.

## Web chats

ChatGPT, claude.ai and Gemini keep your conversations on their servers, so Cortex gets them in two other ways:

- **Import an export** (ChatGPT, Claude): `cortex import FILE`, or the panel's **Web chats** section. Importing the same file twice changes nothing.
- **Browser extension** (ChatGPT, Claude, Gemini; Chrome, Edge, Brave): the [`extension/`](extension/README.md) folder, loaded unpacked (it is not in any browser store yet). It talks only to Cortex on `127.0.0.1` and proves itself with a pairing code.

Both write the same notes as every other source, and a conversation that arrives both ways is one note. Details, limits and the pairing flow: [docs/web-chats.md](docs/web-chats.md) and [extension/README.md](extension/README.md).

## Summaries

Optional, and **off by default**. When a conversation has been quiet for a while, Cortex can have an AI tool write a short summary and store it at the top of the note. The text of the conversation is piped to the tool you choose (Claude Code or Codex CLI, or a custom command you write in the config file), which sends it to its own AI provider and uses your account there. Cortex itself contacts nothing. Do not switch this on for projects whose conversations must not go to that provider. See [docs/summaries.md](docs/summaries.md) for the exact commands, limits and what is locked down.

## Configuration

Optional: Cortex works without a config file. It is a JSON document in the OS config directory (`~/.config/cortex/config.json` on Linux, `%AppData%\cortex\config.json` on Windows, `~/Library/Application Support/cortex/config.json` on macOS), and the panel writes it when you change a setting.

```json
{
  "vault": "/home/me/notes/Cortex",
  "disabled_sources": ["claude-code"],
  "excluded_projects": ["secret-client"],
  "redact_patterns": ["ACME-[0-9]{8}"],
  "paused": false
}
```

Every field, including `extension_token` and the `summaries` block, is in [docs/configuration.md](docs/configuration.md). `run` also keeps a log, `cortex.log`, in the same folder.

## Privacy & security

- **Local only.** Everything stays on your machine. The one optional exception is [summaries](#summaries).
- **No network by default.** Cortex never checks for updates or contacts the Internet. The panel listens on `127.0.0.1` and only accepts connections from your own machine.
- **Redaction.** Before a note is written, secrets are replaced with `[REDACTED]`: private key blocks, API keys and tokens with well-known prefixes, JSON Web Tokens, `Authorization` header values, values assigned to names such as password or secret, `--password VALUE` style flags, passwords in URLs, and any patterns you add. It is best-effort and will miss things: be careful with a synced or shared vault. Details in [docs/privacy.md](docs/privacy.md).
- **Tool output and model thinking are not saved**; tool calls are kept as one line each.
- **Notes are data.** They can quote text written by third parties; an agent reading the vault should treat them as data, not instructions.
- **Pairing code.** The extension's `extension_token` in the config file is like a password: whoever has it, and can reach `127.0.0.1`, can add notes to your vault.

## Update / Uninstall

Cortex never checks for updates. To update, run the install command again: the new binary replaces the old one and takes over from a running Cortex. To uninstall:

- **Windows:** `& "$env:LOCALAPPDATA\Programs\Cortex\cortex.exe" uninstall` (add `-purge` to also remove the config file and the log).
- **macOS / Linux:** turn off "Start Cortex when I log in" in the panel, quit Cortex, and delete the binary (`rm ~/.local/bin/cortex`).

The vault is never removed; delete it yourself if you want the notes gone. Every edge case: [docs/install.md](docs/install.md#update).

## Build from source

You need Go, at the version given in [`go.mod`](go.mod).

```sh
go build -o cortex ./cmd/cortex
./cortex
```

`scripts/release.sh VERSION OUTDIR` builds and packages every release asset, reproducibly. Windows build flags, the embedded resources and how `cortex.exe` behaves in PowerShell: [docs/build-and-run.md](docs/build-and-run.md#build-from-source).

## Status & roadmap

Early development. Cortex captures conversations from the sources above, either once with `cortex sync` or continuously with `cortex run`. Honestly:

- **Verified against real data:** Claude Code sessions and, partly, Copilot Chat session files (without messages). Everything else was written from source code or documentation and tested with invented samples.
- **Web chats:** the extension's selectors come from open-source exporters and have **not been tested against the live sites** or in a real browser; the sites change their HTML without notice. The import formats have not been tried against a real export.
- **Installers:** `install.sh` and `install.ps1` were tested against locally served fake release assets (download, checksum, refusal of a corrupted file), but never against a published release, and `install.sh` never on macOS. The Windows install step, tray icon and start at login have not been verified by hand on a real Windows machine (the test suite and builds run on Windows in CI). The macOS Gatekeeper text has not been checked on a Mac.
- **Not yet:** code-signed releases and an extension in the browser stores.

Bug reports with a bit of the log are the most useful help right now.

## FAQ

**Does anything leave my computer?**
No, unless you switch on summaries, which send the conversation text to the AI tool you choose. Cortex makes no other network connections: it listens only on `127.0.0.1`.

**Where are my notes?**
In the vault folder, `~/Cortex` by default. Change it in the panel. Notes are plain Markdown; [here is the format](docs/notes.md).

**Can I edit or move the notes?**
Yes. Text outside the `cortex:start` and `cortex:end` markers is kept, and renamed or moved notes are found again through the `session` line in the frontmatter.

**What if I delete a note?**
Cortex writes it again the next time its conversation changes or Cortex restarts. To stop capturing something for good, turn its project or source off in the panel. See [docs/notes.md](docs/notes.md).

**Does Cortex capture my passwords?**
It redacts common secret formats before writing, but redaction is best-effort. See [docs/privacy.md](docs/privacy.md).

**Why does Windows or macOS warn me about it?**
Releases are not code-signed yet. See [docs/install.md](docs/install.md).

**Can I stop it capturing a project?**
Yes: turn the project off in the panel, or list it in `excluded_projects`. Notes already written are never deleted.

## Contributing

Issues and pull requests are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md). Before sending a change:

```sh
go vet ./...
go test ./...
cd extension && node --test
```

Please use the [bug report template](https://github.com/marioolf/cortex/issues/new?template=bug_report.md) and remove private content from anything you paste.

## License

[MIT](LICENSE). The licences of the Go modules and the Go standard library compiled into the program are in [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md) (generated by `scripts/licenses.sh`, which CI checks against the committed file); every release archive contains that file.
