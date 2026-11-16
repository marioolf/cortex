# Build from source and run

[Back to the README](../README.md)

## Build from source

You need Go, at the version given in `go.mod`.

```
go build -o cortex ./cmd/cortex
./cortex
```

On Windows, build with `-ldflags "-H windowsgui"` so that starting Cortex at
login or by double click opens no console window (this is how releases are
built; `scripts/release.sh VERSION OUTDIR` builds and packages all of them,
reproducibly: timestamps come from `SOURCE_DATE_EPOCH`, by default the commit time,
and the extension zip carries the release version, without any `-suffix`, in its
copy of `manifest.json`):

```
go build -ldflags "-H windowsgui" -o cortex.exe ./cmd/cortex
```

Cortex then runs without a window and is stopped from the tray menu or the
panel's "Quit Cortex" button. A program built this way does not make PowerShell or an interactive `cmd`
prompt wait: `cortex.exe sync` typed there can return to the prompt at once,
print its summary a moment later, and leave no exit status behind. To wait for
it (in a script, say), use `Start-Process -Wait -NoNewWindow -PassThru .\cortex.exe -ArgumentList 'sync'`
in PowerShell (the result's `ExitCode` is the status), or `start /wait cortex.exe sync` in `cmd`. `run` writes
a log to `cortex.log` next to the config file (see [Configuration](configuration.md); it is started
over when it passes 1 MB, and its first line says which version wrote it). The
log is opened before anything else can fail, so a start that fails (port taken,
unreadable config) leaves its error there, also on the very first run, which
creates the config folder for it. A start that fails with no console to print
on (the Windows build) also shows a message box with the error and the log's path.

## Run

Running `cortex` with no arguments is the same as `cortex run`. It saves every
conversation once, then checks again every 30 seconds, and opens the control
panel in your browser. The panel is served at `http://127.0.0.1:47821/` and
only accepts connections from your own machine. Options for `run`:

- `-no-browser`: do not open the panel.
- `-port N`: use another port.
- `-no-autostart`: see below.
- `-no-tray`: no notification-area icon (Windows).
- `-config FILE`: use another config file. (`sync`, `import`, `summarize`, `mcp`,
  `install` and `uninstall` accept it too.)

If Cortex is already running from the same file, starting it again just opens
the panel. If the running Cortex is a different file (a newer download while the
installed copy runs), `run` takes over: see [Update](install.md#update). If the running Cortex
cannot be asked, `run` opens its panel and exits, and the log says why. If the
port is held by some other program, `run` says so, logs it and exits with an
error; pass `-port` to use another one. A problem in the config file's
`summaries` block does not stop `run`: see [Summaries](summaries.md).

The panel shows the last sync and any warnings, and lets you:

- pause and resume capturing;
- change the vault folder, or open it in your file manager;
- turn sources on and off (the web ones too);
- import a ChatGPT or Claude export and pair the browser extension (see [Web
  chats](web-chats.md));
- turn projects on and off (conversations outside any project are always
  saved);
- install Cortex on Windows (a banner at the top, until it is installed);
- choose whether Cortex starts when you log in (on Windows only from the
  installed copy: elsewhere the switch is disabled with "Install Cortex first",
  and the API answers 409; the switch shows "on" only if the registered entry
  points at the running file);
- connect your AI tools to the vault (see [MCP](mcp.md));
- switch on summaries of finished conversations (see [Summaries](summaries.md); off by default);
- quit Cortex.

Turning something off stops capturing it. Notes already written are never
deleted.

The first time `run` starts (when there is no config file yet), it writes the
default config and, on macOS and Linux, registers itself to start at login with
`run -no-browser`: a launch agent in `~/Library/LaunchAgents` on macOS, an XDG
autostart entry in `~/.config/autostart` on Linux. On Windows it registers
nothing at that point unless it is already running from its installed place
(see [Install](install.md)): installing writes the registry's `Run` key for the installed
copy. Pass `-no-autostart` to skip the registration, or turn it on and off in
the panel at any time.

When Cortex runs on Windows, it also reads the sessions that Linux-side tools
keep in the home directories of WSL distros that are running at that moment. It does not
start distros that are stopped.

`cortex sync` does one round and exits, writing one note per session. It exits
with status 1 if any note could not be written. The default vault is
`~/Cortex`; use `-vault DIR` to choose another folder for that run without
saving it. Running it again only updates what changed. A paused Cortex does not
sync.

