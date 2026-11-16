# Configuration

[Back to the README](../README.md)

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
  "paused": false,
  "extension_token": "...",
  "summaries": {
    "enabled": false,
    "tool": "claude",
    "command": [],
    "projects": ["myapp"],
    "idle_minutes": 15,
    "max_per_hour": 20
  }
}
```

- `vault`: where notes are written (default `~/Cortex`; `sync -vault` overrides
  it for one run).
- `disabled_sources`: sources that are not captured.
- `excluded_projects`: project names whose sessions are not saved.
- `redact_patterns`: extra regular expressions to redact, on top of the
  built-in ones.
- `paused`: capture nothing until set back to `false`.
- `extension_token`: the browser extension's pairing code. `run` creates it when
  it is missing; the panel can replace it. Treat it like a password: whoever
  has it, and can reach `127.0.0.1` from a browser extension or a program, can
  add notes to your vault.
- `summaries`: see [Summaries](summaries.md). `enabled` (default `false`), `tool` (`""`,
  `"claude"`, `"codex"` or `"custom"`), `command` (the argv for `"custom"`, only
  settable here), `projects` (names of the projects to summarise), `idle_minutes`
  and `max_per_hour` (0 or missing means 15 and 20; only settable here). A
  `summaries` block that cannot be used loads as "off" with a warning (see
  [Invalid summaries settings](summaries.md#invalid-summaries-settings)); `"custom"` needs a command whose first element
  is an absolute path or a bare program name.

The panel writes this file when you change a setting, starting from what is in
the file at that moment, so edits made by hand while Cortex is running are kept
(unless the file no longer parses, in which case the panel's in-memory settings
are written over it). They take effect the next time Cortex starts, or at the
next change made in the panel. `-config FILE` on `run`, `sync`, `import`,
`summarize`, `mcp`, `install` and `uninstall` uses a different file. `run` also keeps its log, `cortex.log`, in the same folder.

