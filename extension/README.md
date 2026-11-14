# Cortex Saver (browser extension)

Saves the ChatGPT, Claude or Gemini conversation you have open in a tab to Cortex,
automatically, while you chat. Chrome, Edge and Brave (Manifest V3). There is no
build step and no dependency: the folder is the extension.

It lives in the `extension/` folder of the Cortex repository (and of a release). It
is **not in any browser store yet**: you load it unpacked, as below.

## Install (unpacked)

1. Open `chrome://extensions` (Edge: `edge://extensions`), turn on **Developer mode**.
2. Click **Load unpacked** and choose this `extension/` folder.
3. Pin the icon if you like. A red `!` on it means Cortex is not reachable or the
   pairing code is wrong.

## Pair it with Cortex

1. Start Cortex (`cortex run`) and open its panel.
2. In the panel's **Web chats** section, under **Browser extension**, copy the
   pairing code (the **Copy** button). The first `cortex run` creates it; **Regenerate**
   replaces it, and the extension stops working until you paste the new one.
3. Open the extension's **Options**, paste the code, leave the port at `47821`
   unless you started Cortex with `-port`, and press **Test connection**. It says
   "Connected (Cortex x.y)", "Wrong pairing code" or "Cortex is not running".
The port is shown in the same panel section.

Settings (code, port, per-site switches, global pause) are kept in
`chrome.storage.local`, never in sync storage, so the code stays on this machine.

## What it reads and sends

- It reads only the conversation open in the current tab, and only on a page with a
  conversation id in its address (`chatgpt.com/c/<id>`, `claude.ai/chat/<uuid>`,
  `gemini.google.com/app/<id>`). Other pages are left alone.
- It sends that conversation (source, id, title, page URL, messages as Markdown) to
  `http://127.0.0.1:<port>/api/ext/ingest` with your pairing code, and nothing
  anywhere else. No analytics, no remote code. The page is never modified.
- It does not capture reasoning ("thinking") panels or tool and search interface,
  and it writes images as `[image]`.
- It sends about 3 seconds after the page stops changing, not while the assistant is
  still answering (when that can be detected), not more than once every 5 seconds,
  only if the content changed, and once more when you leave the tab or page. If Cortex
  is down it keeps the last conversation in memory and retries with a growing delay
  (10 s up to 5 min).

Permissions: `storage` (settings); host access to `http://127.0.0.1/*` (any port on
the loopback address, because the port is configurable) so that the background worker
can reach Cortex; content scripts on the four chat sites. No `tabs`, no `<all_urls>`.

## Limits

The extension depends on each site's HTML, which the sites change without notice.
The selectors come from open-source exporters and were written from their
descriptions; **they have not been tested against the live sites**. What is tested
(`node --test`, no browser): the HTML to Markdown converter, each extractor against
invented HTML shaped like the hooks it uses, the change fingerprint and backoff, and
that the manifest only references existing files and asks for the permissions above.
Not tested at all: the content script's timing, the worker, the popup and options
pages in a real browser, and the two together: Cortex's side of the API is covered by
its own Go tests (with invented requests), but the extension has never talked to a
running Cortex from a browser.

## What Cortex does with it

Cortex answers on `127.0.0.1` only, and the extension routes are authenticated by the
pairing code alone (header `X-Cortex-Extension`); the panel's own token is not involved.

- `GET /api/ext/ping` answers `{"ok":true,"version":"..."}`, or 403 for a wrong code.
- `POST /api/ext/ingest` takes `{"source","id","title","url","messages":[{"role","text","time"?}]}`
  and answers `{"result":"created"|"updated"|"unchanged"|"skipped"}`. 400 means invalid
  (unknown source, bad id, no messages, a `url` that is not an https address of that
  site, unknown fields), 403 a wrong code, 413 a body over 5 MB. `skipped` means Cortex is
  paused or that site is switched off in the panel; nothing was written.
- A request whose `Origin` is not `chrome-extension://` or `moz-extension://` is refused,
  and Cortex sends no CORS headers, so a web page cannot use these routes.
- Each push replaces the whole note body of that conversation (it is not appended to),
  so the note always matches what the page showed. Secrets are redacted like everywhere
  else. The extension cannot see message times, so a new note is dated when Cortex first
  received it.
- A conversation imported from an export and later pushed with the same id is the same
  note.

If a site stops working, check what a message looks like in the browser's
developer tools and fix only that site's file under `content/sites/`.

## Fixing an extractor

Each site is one small file in `content/sites/` with the same interface:
`matches(location)`, `conversationId(location)`, `title(document)`,
`messages(document)` (a list of `{role, text}`), plus optional `isStreaming(document)`
and `skip(element)`. The file's header comment says which attributes and classes it
relies on. To fix one: copy a message's HTML into a test in `test/sites.test.js`, make
the test fail, adjust the selectors, run `node --test` in this folder. Add the file to
`manifest.json` if you add a site. Node 20 or newer is enough; nothing is installed.

## Layout

```
manifest.json          permissions, content scripts, CSP
background.js          service worker: the only code that talks to Cortex
content/content.js     watches the page, debounces, hands conversations to the worker
content/sites/*.js     one extractor per site
lib/md.js              HTML to Markdown on a minimal node interface
lib/util.js            fingerprint and backoff
lib/settings.js        storage.local access and defaults
options/ popup/        the two pages
icons/                 generated marks (icon.svg is the source drawing)
test/                  node --test, with a tiny fake DOM
```

Firefox: the code already uses `browser ?? chrome`. A Firefox build needs a manifest
with `background.scripts` instead of `service_worker` (and an `importScripts`
replacement: list `lib/settings.js` first) and `browser_specific_settings.gecko.id`.
Not done yet.
