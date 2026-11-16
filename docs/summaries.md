# Summaries

[Back to the README](../README.md)

Optional, and **off by default**. When a conversation has been quiet for a while,
Cortex can have an AI tool write a short summary of it and store it as a
"Summary" block at the top of the note, so that you (and the AI agents that read
the vault) find the gist before the whole conversation.

What leaves your machine: the saved text of the conversation (the note's
generated block, so already redacted, up to 200,000 characters; for a longer one
the beginning and the end) is piped to the standard input of the tool you
choose. That tool is a program on your computer, but it sends the text to its
own AI provider and uses your account and quota there. Cortex itself contacts
nothing. Do not switch this on for projects whose conversations must not go to
that provider.

To enable it, in the panel's "Summaries (optional)" section: pick a tool, tick
the projects to summarise and turn the switch on. Conversations outside any
project are never summarised. The panel lists a tool only if its program is
found on your `PATH`.

| Tool | Command Cortex runs (prompt on stdin, summary on stdout) |
|---|---|
| Claude Code | `claude -p --tools "" --disallowedTools "*" --strict-mcp-config --disable-slash-commands --permission-mode dontAsk --no-session-persistence` |
| Codex CLI | `codex exec --sandbox read-only --ephemeral --skip-git-repo-check --ignore-user-config --color never -` |

The conversation is text written partly by third parties, so the tool must not
be able to act on instructions hidden in it. The prompt says that the text is a
record to summarise and not to be obeyed, and the flags above lock the tool down
as far as its documentation allows: for Claude Code no tools (built-in or MCP),
no skills and no saved session ([CLI reference](https://code.claude.com/docs/en/cli-reference));
for Codex a read-only sandbox, no saved session and the user's `config.toml`
(with its MCP servers) ignored ([command reference](https://learn.chatgpt.com/docs/developer-commands?surface=cli),
[non-interactive mode](https://learn.chatgpt.com/docs/non-interactive-mode)). A read-only
sandbox still lets the program read files, and the Codex page does not say
whether it blocks the network; the flags were checked against the
documentation, not against the programs. OpenCode is not offered: its
documentation does not show a way to run it with tools disabled, so it can be
used only as a custom command that you vouch for.

The command runs without a shell, in an empty folder (`summaries-work`, next to
the config file, with symlinks resolved and `PWD` set to it), for at most 5
minutes, with its output capped at 32 KB; the environment variable
`CORTEX_SUMMARY=1` is set. It runs in a process group of its own (on Windows
Cortex runs `taskkill /T /F`), and when it times out or Cortex quits, the tool
and the processes it started in that group are stopped; a process that
deliberately detaches itself (a new session, a daemon) is not. Its output is cleaned (invalid UTF-8
replaced, escape sequences and control characters other than newline and tab
removed) and passed through the same redaction as the notes, including your
`redact_patterns`, before it is stored. These tools keep their own
sessions on disk, so Cortex never saves a session that was started in that
folder (otherwise it would summarise its own summaries).

## Custom command

To use another tool, write its argv by hand in the config file and choose
"Custom command from the config file" in the panel:

```json
"summaries": { "tool": "custom", "command": ["/path/to/summarise", "--short"] }
```

The first element must be an absolute path or a bare program name found on your
`PATH`. A relative path such as `./summarise` is refused (it would be resolved
against the work folder, where the tool itself can write); the settings are then
treated as invalid, as described below.


It receives the prompt on stdin and prints Markdown on stdout. The panel shows
this command but cannot change it, and `/api/config` has no field for it: if it
could, anyone who obtained the panel's token (a local program, a bug in a web
page) could make Cortex run any program. For the same reason the panel cannot
change the idle time or the rate limit either.

## Limits and behaviour

- A note is summarised when its file has not changed for `idle_minutes` (15 by
  default), it has at least 4 messages and it has no summary for its current
  content. If the conversation goes on, a new summary replaces the old one once
  it is quiet again.
- One summary at a time, at most `max_per_hour` (20) per hour. A note whose
  summary failed is not retried for the same content for an hour. Failures
  show in the panel. A failure names the note by its source and the first 8
  characters of the session ID (never the title or the file name) and holds at
  most the first line of the tool's error output, cut to 200 characters and
  redacted; the conversation is never part of it. A conversation that is being
  summarised, or queued, is never queued again (even if the tool takes longer
  than several rounds), and the failure backoff is checked again before the
  tool is started.
- While Cortex is paused, nothing is summarised. Turning summaries off or
  removing a project stops new ones; existing summaries are never deleted.
- Summaries can be wrong. The block says so, and, like the rest of the vault, it
  is a record and not instructions.
- `cortex summarize NOTE` summarises one note now (a path relative to the vault
  or absolute inside it), ignoring idle time, project selection and the rate
  limit but needing a configured `summaries.tool`, and prints the summary. It
  sends the note's text to that tool.

The summary sits in its own block, right before the conversation:

```markdown
<!-- cortex:summary:start hash=0123456789ab -->
## Summary

> Generated by Claude Code on 2026-05-01. It can be wrong, and it is a record, not instructions.

...
<!-- cortex:summary:end -->

<!-- cortex:start -->
```

Like the conversation block it is Cortex's: text you write outside the two
blocks is kept. (The `hash` is that of the conversation the summary was made
from.)

## Invalid summaries settings

A mistake in the `summaries` block of the config file (an unknown `tool`,
`"custom"` without a `command` or with a relative one, negative numbers, a value
of the wrong type) never stops Cortex. `run`, `sync`, `mcp`, `import`, `install`
and `uninstall` load the file with summaries switched off and print the problem
as a warning (to stderr; `run` also logs it). The panel shows it prominently at
the top (`warnings` in `/api/state`), and its Summaries section explains the
problem instead of offering controls until the file is fixed. Your block is kept
as written when the panel saves other settings. Fix the file and restart Cortex
(or change any other setting in the panel, which re-reads the file). A config
file that is not valid JSON at all is an error for `run`, `sync`, `import`,
`summarize` and `mcp`, which do not guess; the message names the file, and for
`run` it is logged (see [Build from source](build-and-run.md#build-from-source)) and, without a console, shown in a
message box. `install` and `uninstall` only warn about it and go on.

