# Contributing to Cortex

Thanks for your interest. Bug reports and pull requests are welcome.

## Reporting a problem

Use the [bug report template](https://github.com/marioolf/cortex/issues/new?template=bug_report.md). The most useful reports say which source (Claude Code, Copilot, ...) produced a wrong note and include a few lines of `cortex.log`. Remove private content first: conversation text, paths and project names you do not want to share, and the `extension_token` from the config file.

For security problems, please do not open a public issue with details; contact the maintainer through the address on the GitHub profile.

## Building and testing

You need Go at the version in `go.mod`, and Node 22 or newer for the extension's tests. There are no other dependencies.

```sh
go build -o cortex ./cmd/cortex
gofmt -l .            # prints nothing when the code is formatted
go vet ./...
go test ./...
cd extension && node --test
```

CI runs the Go checks on Linux, Windows and macOS, and the extension tests on Linux. Releases are built with `scripts/release.sh`.

## Guidelines

- Keep changes small and focused, with a test where the behaviour is testable.
- Parsers for a tool's session format are written from its source or documentation; say in the pull request what you checked against real data and what you did not.
- Do not add dependencies without discussing it first. The licences of everything compiled into the program are listed in `THIRD_PARTY_LICENSES.md` (regenerate with `scripts/licenses.sh`).
- Do not weaken the control panel's security settings (Content Security Policy, request guard, routes) without a clear reason in the pull request.
- Test data must be invented: never commit real conversations.

By contributing you agree that your contribution is licensed under the MIT licence of the project.
