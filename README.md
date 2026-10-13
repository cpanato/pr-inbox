# PR Inbox

A local GitHub pull request inbox. The Go server reads the existing GitHub CLI
authentication and serves an embedded browser interface on loopback.

## Current implementation

Milestone 1 is implemented: authenticated **To review** and **My PRs** queues,
organization filters, capped discovery, explicit direct-request-only coverage,
and a read-only local UI. Pull request details, checks, diffs, saved preferences,
and review submission are not implemented yet.

## Requirements

- Go 1.22 or newer
- Node.js 24.x and npm (for building the embedded frontend)
- GitHub CLI (`gh`) authenticated to github.com

## Build and run

```sh
gh auth login
make build
./dist/pr-inbox
```

Useful options:

```sh
./dist/pr-inbox --no-open
./dist/pr-inbox --org ossf --org sigstore
./dist/pr-inbox --read-only
./dist/pr-inbox doctor
```

The server binds only to `127.0.0.1` on an OS-assigned port. The browser launch
link contains a one-launch secret in its URL fragment; the secret is exchanged
for a local, HttpOnly, SameSite session and removed from the address bar. GitHub
credentials stay in the Go process and are never sent to the browser.

## Development

```sh
make test
```

`make build` and `make test` build the frontend before running Go commands because
the generated frontend is embedded in the binary.

## Releases

Releases are built by GitHub Actions when a `v*` tag is pushed. GoReleaser
produces archives for Linux, macOS, and Windows on amd64 and arm64, plus a
SHA-256 checksum file. To test a release locally, run `make release-snapshot`
with GoReleaser installed.
