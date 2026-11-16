# Web chats

[Back to the README](../README.md)

ChatGPT, claude.ai and Gemini keep your conversations on their servers, not on
your disk, so Cortex gets them in two other ways. Both write the same notes as
every other source (`Chats/chatgpt/`, `Chats/claude-ai/`, `Chats/gemini/`), and
a conversation that arrives both ways is one note, because the note is found
by the source and the conversation's id (the same id under two sources is two
notes). A web note is dated when Cortex first received it if the conversation
carries no times (the extension cannot see them). The `url:` line in its
frontmatter is written only when the extension creates the note: a note that
already existed, for example one made by importing an export, does not get it
later, even if the extension pushes that conversation afterwards.

## Import an export

ChatGPT: Settings, Data controls, Export data. Claude: Settings, Privacy,
Export data. You get an email with a zip. An export is a snapshot of that day:
import a newer one later to bring conversations up to date (those that
changed are updated, the rest are left as they are). Gemini has no export that
Cortex reads.

```
cortex import FILE [-vault DIR] [-config FILE]
```

`FILE` is the zip as downloaded or the `conversations.json` inside it. It
prints `N conversations: a created, b updated, c unchanged, d failed, e could
not be read` and exits with status 1 if any conversation could not be written
or the file is not a ChatGPT or Claude export. Conversations in the file that
could not be used are counted in `e`, and the others are saved. The same is
available in the panel's "Web chats" section ("Import an export": choose the
file and press Import; files up to 2 GiB, one import at a time).

The file is read as a stream, one conversation at a time, so memory use does
not depend on the size of the export. These limits protect against hostile or
broken files: a conversation of more than 64 MiB of JSON is skipped (and
counted in `e`) without being loaded; more than 64 MiB of blank space between
two conversations stops the import with an error; one file inside a zip may not
decompress to more than 2 GiB, nor all the files together to more than 8 GiB. A
conversation without an id is skipped, and when one export holds the same id
twice the first one is kept and the later ones are skipped; both are counted in
`e` and named in the messages printed. Any one message longer than 1,000,000
characters is cut there, whatever its source (also for collected sessions and the
extension), and ends with a line `[… N more characters not saved by Cortex]`.

An import is something you ask for by hand, so it ignores Pause and the
source switches: a paused Cortex, or one with ChatGPT turned off, still saves
what you import. Importing the same file twice changes nothing.

## Browser extension

The `extension/` folder of this repository (and of a release) is a Chrome, Edge
and Brave extension that sends the ChatGPT, Claude or Gemini conversation open
in a tab to Cortex while you chat. It is not in any browser store yet: load it
unpacked (`chrome://extensions`, Developer mode, Load unpacked, choose the
folder). See `extension/README.md` for what it reads and sends.

It talks to the running `cortex run` on `127.0.0.1`, and proves itself with a
pairing code. The first `cortex run` creates the code and saves it in the config
file as `extension_token`; the panel's "Web chats" section shows it with a Copy
button, the port, and a Regenerate button (the old code stops working at once).
Paste the code and the port into the extension's options and press Test
connection. Until a code exists the extension's routes answer 403 to everybody.
The code can also stop Cortex (`POST /api/ext/quit`) and tells a caller where
Cortex runs from (`GET /api/ext/ping` also returns `exe`, the running program's
path). That is how `install`, `uninstall` and a newer download replace a
running copy without the panel's token, which only the page knows; anyone who
can read the config file could end the process anyway. Treat the code like a
password.

The code is the only thing the extension needs: the panel's own token is not
involved. Requests that carry an `Origin` other than an extension's are refused
and no CORS headers are sent, so a web page cannot post conversations even if
it learned the code. Unlike an import, the extension respects Pause and the
source switches: while paused, or with a web source off, Cortex answers
`skipped` and writes nothing. The extension does not count that as saved: its
popup says "Cortex is paused or this site is turned off in Cortex" and it tries
again every 60 seconds while the tab is visible, so the conversation is saved
once you resume. Without a pairing code in its options it says "Not paired
yet" and does not try at all.

