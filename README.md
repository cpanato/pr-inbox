# PR Inbox

A local GitHub pull request inbox. The Go server reads the existing GitHub CLI
authentication and serves an embedded browser interface on loopback.

## Current implementation

Milestone 1 is implemented: authenticated **To review** and **My PRs** queues,
organization and repository filters, capped discovery, explicit direct-request-only coverage,
and a read-only local UI. Pull request details, checks, diffs, saved preferences,
and review submission are not implemented yet.

## Build and run

```sh
gh auth login
make build
./dist/pr-inbox
```

Useful options:

| Command | What it does |
| --- | --- |
| `./dist/pr-inbox` | Starts the local server and opens the inbox in your default browser. |
| `./dist/pr-inbox --no-open` | Starts the server and only prints the launch URL, without opening a browser. Useful over SSH or to pick a specific browser. |
| `./dist/pr-inbox --org ossf --org sigstore` | Limits discovery to the given organizations. Repeat `--org` for each one. Without it, PRs from every organization and account you can see are searched. |
| `./dist/pr-inbox --read-only` | Disables write operations. The current inbox is already read-only, so this is for forward compatibility. |
| `./dist/pr-inbox doctor` | Checks that `gh` is installed and authenticated and that the GitHub API is reachable, prints the account, then exits without starting the server. `--doctor` works too. |

Pull requests from archived repositories are never listed. Inside the UI, the
organization and repository selectors narrow the loaded queue (the repository
list only shows repositories of the selected organization), unlike `--org`,
which narrows what is fetched from GitHub.

The server binds only to `127.0.0.1` on an OS-assigned port. The browser launch
link contains a one-launch secret in its URL fragment; the secret is exchanged
for a local, HttpOnly, SameSite session and removed from the address bar. GitHub
credentials stay in the Go process and are never sent to the browser.

## Development

Building from source needs Go 1.27+, Node.js 24.x with npm (see `.nvmrc`) for
the embedded frontend, and an authenticated GitHub CLI (`gh`).

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
