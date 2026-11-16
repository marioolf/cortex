# What the notes look like

[Back to the README](../README.md)

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

