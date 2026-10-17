<p align="center">
  <img src="web/public/logo.svg" alt="PR Inbox logo" width="96" height="96">
</p>

# PR Inbox

A local GitHub pull request inbox. The Go server reads the existing GitHub CLI
authentication and serves an embedded browser interface on loopback.

![PR Inbox showing the To review queue with CI status, filters and review reasons](assets/screenshot.png)

![The same inbox on a GitLab tab, showing merge requests](assets/screenshot-gitlab.png)

The screenshots use sample data (`--demo`). With more than one provider, each gets its own tab.

## Current implementation

Milestone 1 is implemented: authenticated **To review** and **My PRs** queues,
organization and repository filters, CI status labels, capped discovery, explicit
direct-request-only coverage, and a local review workspace. Clicking a PR/MR title
opens a detail panel with its description (plain text), author, branches, head
commit, and CI summary for that commit. The panel is available for GitHub,
GitLab, and demo data. A file browser shows unified diffs pinned to the inspected
revision, with additions, deletions, renames, and a plain-text option. The
read-only discussion timeline shows GitHub general comments, submitted reviews,
and inline review comments, or GitLab notes, file discussions, and system
activity. The checks panel shows individual GitHub check runs and commit statuses,
or the selected GitLab pipeline, jobs and trigger jobs, with results and links to
the provider's logs. Files with complete patches can be explicitly marked **Viewed**;
progress is saved on this device and restored when details are reopened.

Diffs are limited to 300 files and 5 MiB of patch text, with a 256 KiB / 10,000-line
limit per file. Binary, missing, oversized, and incomplete patches are labeled
explicitly and linked to the provider. GitHub comparisons use captured base and
head SHAs; GitLab uses a matching stored diff version. New pushes do not silently
replace the displayed revision. Use **Reload details** to inspect a newer one.
GitLab searches the most recent 100 diff versions; an unavailable version asks
you to reload details rather than substituting another diff. Inspected revision
references are kept only in memory (the most recent 64 per provider).

Discussion is loaded independently of the pinned diff and reflects the current
conversation on the provider. Entries show author, date, review state and file /
commit context where available, with links for reading threads on the provider.
Bodies are plain text: HTML and remote images are never rendered. **Load more
discussion** fetches another page; loaded entries are sorted chronologically and
may move as more pages arrive. **Refresh discussion** starts again at page one.
These pages are live reads, not an atomic history snapshot; concurrent edits or
deletions may shift pagination.

Discussion limits are 10 pages, 100 messages and 512 KiB of body text per page,
and 16 KiB per message. GitHub fetches 20 items from each of its three streams
per page; GitLab fetches 20 discussions (which may contain several notes).
Omissions and shortened bodies are labeled. A failed page retains the already
loaded entries and can be retried without advancing pagination. Pagination may
require a final empty page to confirm the end. GitHub pending reviews and
thread resolution state remain on GitHub; GitLab includes note resolution state
when supplied by the API.

Checks are loaded for the inspected commit and refreshed independently of the
queue. GitHub combines check runs and external status contexts, up to 200 results.
GitLab captures the matching pipeline ID and project when details are opened,
verifies its SHA before reading jobs, and includes trigger jobs via the compatible
`bridges` endpoint. Child-pipeline jobs are available through provider links.
Reload details to select a newer GitLab pipeline. Incomplete lists and failed
refreshes are explicit; skipped, neutral, canceled, manual and allowed-to-fail
results remain distinct. Check results do not imply that a change can be merged.

Viewed markers are separated by provider, host, account and PR/MR, and pinned to
both the head and base of the inspected diff. Changed revisions start with no
viewed files and show a notice when another revision has saved progress. Binary,
omitted and unlisted patches remain pending. Markers contain file paths only;
credentials, descriptions and patches are not saved. Storage defaults to
`pr-inbox/state` under the OS user configuration directory (on macOS,
`~/Library/Application Support`; on Linux, `$XDG_CONFIG_HOME` or `~/.config`;
on Windows, `%AppData%`). Use `--state-dir PATH` to choose another location.
Records are written atomically; updates to separate files are independent, and
concurrent updates to the same marker use the last successful write. Storage
errors are shown without claiming that progress was saved. `--read-only` lets
you read existing progress but disables changes. Nothing is sent to the provider.

The **Local review draft** editor saves an overall review body and intent
(comment, approve or request changes) for the inspected head commit. Click
**Save draft** explicitly; unsaved changes are labeled and closing the panel,
reloading details or leaving the page prompts before discarding them. Drafts
survive app restarts and are separated by provider, host, account and PR/MR.
A changed head starts a separate draft. Earlier drafts can be read and explicitly
copied into the current editor; copying does not save or submit them. Up to 100
earlier commits are listed, most recently updated first.

Drafts have a 64 KiB UTF-8 text limit and are stored as plain text under
`drafts-v1` in the same state directory as viewed markers. Atomic writes, process
locks and version checks protect against concurrent overwrites. A conflict or
storage failure retains the editor text; copy it before choosing **Reload saved
draft**. **Delete draft** asks for confirmation and clears only the saved draft
for the inspected commit, retaining a version record to reject stale writers.
`--read-only` permits reading existing drafts and prevents saves and deletions.
Saving a draft never submits it. GitHub reviews use the separate confirmation flow below;
GitLab uses the provider-specific flow described below.

## Submitting a GitHub review

Save a draft, choose **Preview review submission**, and inspect the confirmation:
account, repository/PR number, full head SHA, action and complete text. Check the
acknowledgment box, then choose **Confirm and submit review**. A preview expires
after five minutes and cannot be reused. Editing or saving another draft requires
a new confirmation. GitLab uses the same preview and explicit confirmation, with different effects described below.

Comments and requests for changes require nonblank text; approval text is optional.
You cannot approve or request changes on your own PR. The server rechecks the
GitHub account, the open PR's head and your pending reviews before the write.
A changed head or existing pending review stops submission and preserves the draft.
Finish pending reviews on GitHub. Review history inspection is limited to 1,000
entries; if completeness cannot be established, continue on GitHub instead.
GitHub remains the authority on eligibility and token permissions; fine-grained
tokens need **Pull requests: write** for submission. See the
[GitHub review API](https://docs.github.com/en/rest/pulls/reviews#create-a-review-for-a-pull-request).

Submission sends an overall review with an explicit `commit_id`. A push may race
the final check: the returned review remains associated with the inspected SHA,
and the app checks the head afterward and reports changes or an unavailable check.
A confirmed review clears only the draft version that was sent and refreshes the
queue and discussion. It does not imply that the PR is ready to merge.

The app records each attempt before the POST and serializes submission with draft
writes across local processes sharing `--state-dir`. It retains the latest
operation per account/provider/PR in `.review-operation` beside the draft records,
including the review ID, action, SHA, body hash and attempt time, without copying
the review text. After a timeout, malformed response, crash or unknown server
response, new submissions are blocked. No POST is automatically retried.
**Check outcome on GitHub** performs read requests and matches actor, commit,
action, body hash and submission time (within two minutes before to five minutes
after the attempt). Only one matching review can confirm the outcome; absent or
ambiguous matches remain unknown. Inspect GitHub directly when necessary; do not
repeat an uncertain submission manually without checking it. Keep the state
directory: clearing it or using another directory removes local duplicate protection.
Independent devices do not share operation tracking.

`--read-only` blocks submission and reconciliation as well as local draft writes.
In `--demo`, the GitHub tab simulates the complete flow without any remote write;
both provider tabs simulate submission without remote writes.

## Submitting a GitLab review

GitLab uses the same saved-draft confirmation and operation journal as GitHub.
The confirmation shows the exact summary note, including the inspected SHA and
an operation reference used to match the result after a lost response.
Credentials need the **api** scope for writes (`read_api` remains sufficient for
the inbox). Credentials are scoped to the configured GitLab host, and redirects
are never followed.

- **Comment:** posts a merge request summary note. It does not change reviewer state.
- **Approve:** posts the summary note, then requests a formal approval with the
  inspected `sha`. These are separate writes. If the approval fails or a push
  intervenes, the note can remain published while approval is unconfirmed.
  Password-required approvals must be completed on GitLab.
- **Request changes:** publishes the summary through GitLab's review-publishing
  endpoint and sets the actor's reviewer state to `requested_changes`. The actor
  must already be an assigned reviewer. GitLab can remove the actor's existing
  approval; merge-blocking behavior depends on the instance's tier/settings.
  This endpoint also publishes the actor's pending comments. The app rejects
  existing pending drafts, but drafts created concurrently in another client
  may be published too. The confirmation discloses this effect; do not start
  another pending review during submission.

The app checks identity, open MR state, unchanged head and pending draft notes
before writing. It rejects self-approval/change requests, already-recorded approvals
or change requests, and slash-command lines in the draft to prevent unintended
GitLab quick actions. GitLab notes and change requests lack an atomic head guard:
the captured SHA is recorded in the summary, and a post-write check reports head
changes. The formal approval endpoint additionally enforces `sha` server-side.

A partial or unknown result preserves the draft and blocks another submission.
The result panel links to the summary when available. Reconciliation performs
reads only: it matches the actor, target MR, exact summary hash, operation reference
and time. Approval recovery also requires the same current head and an approval
timestamp near the attempt; missing timestamps remain unknown. Change requests
are confirmed only when the reviewer state is actually `requested_changes`.
An older instance that ignores the state parameter cannot turn a summary note
into a falsely confirmed change request. Follow-up read lists are capped at 1,000
entries; unavailable or incomplete endpoints stop verification. No partial operation
is retried or automatically resumed. Complete unresolved cases on GitLab after
checking what was published. `--read-only` blocks all these mutations.

See the [notes API](https://docs.gitlab.com/api/notes/),
[approvals API](https://docs.gitlab.com/api/merge_request_approvals/) and
[draft review publication API](https://docs.gitlab.com/api/draft_notes/).

## Previously reviewed

The **Previously reviewed** tab lists the most recent confirmed overall review
per PR/MR tracked by this app, separated by provider, host and account. It remains
available after an item leaves **To review** or **My PRs**, and includes closed and
merged items. Each entry shows the reviewed SHA, action, confirmation time and
review link. **Head changed since review** means the current head differs from
that SHA; it does not claim how many commits changed or that a later commit was
reviewed. **Head matches reviewed commit** describes the last successful check.

History loads on demand and is cached in memory for two minutes. **Refresh** reads
the provider again, with at most four concurrent requests, a 20-second per-item
read timeout and a 25-second request budget. Individual failures show **Head check
unavailable**, retain the last successful check time when present, and do not
claim an unchanged head. A failed history reload retains the previously loaded
list with a stale-data notice. Organization/repository/search filters apply to
the loaded history. The `--org` flag narrows inbox discovery; this local history
is account-wide and checks previously tracked targets independently.

History contains locally saved metadata and may remain visible when upstream
access is unavailable. Read-only mode permits loading and refreshing history;
these operations never write to disk or submit a review. Opening an item still
fetches fresh details and applies the normal revision/submission checks.
GitLab target project IDs are saved and revalidated before restoring detail access.

Metadata is saved atomically under `review-history-v1` in `--state-dir`, independently
of later failed or uncertain review attempts. It includes provider/account identity,
target identifiers, title/author/timestamps, reviewed SHA, action and the confirmed
review result; it excludes review bodies, descriptions, patches and credentials.
Only reviews confirmed after this history feature was introduced are tracked.
Older operation journals without target coordinates cannot be reconstructed, and
reviews performed elsewhere are not imported. The latest 200 entries are displayed;
corrupt records or a capped list are explicitly marked incomplete. This is local
follow-up history, not a complete provider review history.

## Snooze and reminders

Each item can be snoozed for 1, 4 or 24 hours, or until a custom local date and
time. Snoozed items are hidden from the loaded queues, including review history;
queue totals still include them. Expand **Snoozed** to open fresh details, cancel
or reschedule an item. The list uses saved metadata and remains available if an
item leaves inbox discovery; it does not assert that the item is still open.

Deadlines are stored as absolute UTC timestamps in `--state-dir`, separated by
provider, host and account. A four-hour snooze still has two hours remaining after
two hours with the machine switched off. Once the deadline is reached, the item
is no longer hidden. If the app was closed, **Snooze ended** appears on the next
opening. Overdue reminders persist until explicitly dismissed or rescheduled;
dismissing a reminder does not hide the item again. Notifications are shown inside
the app; the server does not need to run while the machine is off.

The UI checks deadlines while open and reloads local snoozes every 30 seconds and
when the window regains focus or becomes visible after sleep. Deadline checks use
the server clock. Local state is saved atomically under `snooze-v1` with private
file permissions and a cross-process lock. Only item metadata and deadlines are
stored; no provider write is performed. Up to 500 active or overdue reminders are
retained per account, with deadlines at most one year ahead. Corrupt or unreadable
state produces an explicit error and is not silently replaced. Read-only mode
permits reading snoozes and showing overdue reminders, but blocks changes.

## Roadmap

Milestone 3 is complete; Milestone 5 is the next delivery priority. Milestone 4 is
recorded as backlog only; implementation is not planned. Unchecked items may
change in scope. Milestone numbers identify the themes, not their delivery order.

- [x] **Milestone 1 – Read-only inbox:** authentication, review-requested and authored queues, filters, CI status, coverage labels, links.
- [x] **Milestone 2 – Review workspace:**
  - [x] Read-only PR/MR details: plain-text description, author, branches, head commit and CI summary
  - [x] Read-only discussion timeline with pagination and provider links
  - [x] Diffs pinned to a commit, with large, binary and truncated diff handling
  - [x] Detailed check results and provider links
  - [x] Local file "viewed" markers pinned to the inspected revision
  - [x] Local review drafts
- [x] **Milestone 3 – Overall review writes:**
  - [x] GitHub overall review submission (approve, comment, request changes) after explicit confirmation
  - [x] GitHub checks for unchanged head, account identity, open PR and pending review
  - [x] Persistent operation tracking, outcome reconciliation and refresh after submission
  - [x] GitLab comments, formal approvals and requested-changes state, with explicit provider effects and partial-outcome protection
- [ ] **GitLab support:**
  - [x] Provider-neutral internal model (pull request / merge request)
  - [x] GitLab.com and self-hosted instances (`--gitlab`, repeatable) with merge requests to review, authored merge requests and pipeline status
  - [x] One tab per provider instance in the UI; a single GitHub provider keeps the current layout
- [ ] **Milestone 5 – Personal triage and follow-up (next after Milestone 3):**
  - [x] Previously reviewed PRs/MRs tracked from successful reviews submitted through this app
  - [x] Detect changed heads since the last confirmed review, with explicit freshness and coverage
  - [x] Snooze items until a chosen date, persist deadlines across restarts and show overdue reminders
  - Persist filters, ordering and preferences per account and provider
  - Expand team review-request coverage, with explicit completeness limits
- [ ] **Milestone 4 – Inline review (backlog only; no implementation planned):**
  - Comments on diff lines, including renamed files
  - Local inline drafts tied to file, line and inspected commit
  - Confirmed submission of inline comments with an overall review
  - Thread replies and resolution where supported by the provider
  - Outdated-comment labels and position validation after head changes

## Requirements

- Go and Node.js 24 to build from source (not needed for a release binary).
- To see **GitHub**: the [GitHub CLI (`gh`)](https://cli.github.com/) logged in with
  `gh auth login`, or a `GITHUB_TOKEN` / `GH_TOKEN` environment variable.
- To see **GitLab**: the [GitLab CLI (`glab`)](https://gitlab.com/gitlab-org/cli) logged in
  with `glab auth login`, or a personal access token with the `read_api` scope for reads or `api` for review submission in
  `GITLAB_TOKEN` (see [GitLab](#gitlab)).

You only need the providers you use. GitHub and gitlab.com are tried automatically:
a provider without credentials logs a warning in the terminal and its tab explains
how to log in. If neither has credentials, the program exits with that explanation.

## Build and run

```sh
gh auth login      # and/or: glab auth login
make build
./dist/pr-inbox
```

Useful options:

| Command | What it does |
| --- | --- |
| `./dist/pr-inbox` | Starts the local server and opens the inbox in your default browser. |
| `./dist/pr-inbox --no-open` | Starts the server and only prints the launch URL, without opening a browser. Useful over SSH or to pick a specific browser. |
| `./dist/pr-inbox --org ossf --org sigstore` | Limits discovery to the given organizations. Repeat `--org` for each one. Without it, PRs from every organization and account you can see are searched. |
| `./dist/pr-inbox --gitlab gitlab.example.com` | Also lists merge requests from a self-hosted GitLab. Repeat `--gitlab` for each instance. gitlab.com is added automatically. See [GitLab](#gitlab). |
| `./dist/pr-inbox --no-github` | Skips GitHub, for people who only use GitLab. |
| `./dist/pr-inbox --no-gitlab` | Skips the automatic gitlab.com tab. |
| `./dist/pr-inbox --read-only` | Disables writes, including local viewed markers and review drafts. Existing progress and drafts can still be read. Also disables GitHub review submission and outcome reconciliation. Defaults to off. |
| `./dist/pr-inbox --state-dir PATH` | Chooses the directory for local review progress and drafts. |
| `./dist/pr-inbox --demo` | Runs with built-in sample data instead of GitHub. No `gh` or authentication is needed and no network calls are made, so it is a safe way to try the interface (and how the README screenshot is produced). Can be combined with `--org` and `--no-open`, but not with `doctor`. |
| `./dist/pr-inbox doctor` | Checks that `gh` is installed and authenticated and that the GitHub API (and every `--gitlab` host) is reachable, prints the accounts, then exits without starting the server. `--doctor` works too. |

Pull requests from archived repositories are never listed. Inside the UI, the
organization and repository selectors narrow the loaded queue (the repository
list only shows repositories of the selected organization), unlike `--org`,
which narrows what is fetched from GitHub.

The server binds only to `127.0.0.1` on an OS-assigned port. The browser launch
link contains a one-launch secret in its URL fragment; the secret is exchanged
for a local, HttpOnly, SameSite session and removed from the address bar. GitHub
credentials stay in the Go process and are never sent to the browser.

## GitLab

gitlab.com is added automatically; if no token is found you get a warning and a
tab with login instructions (`--no-gitlab` turns it off). `--gitlab HOST` adds a
self-hosted instance and can be repeated. Only HTTPS is supported.

Create a [personal access token](https://docs.gitlab.com/user/profile/personal_access_tokens/)
with the `read_api` scope for reads or `api` for review submission, and expose it through an environment variable. Tokens
are never written to disk and are only sent to the host they belong to:

| Host | Environment variable |
| --- | --- |
| `gitlab.com` | `GITLAB_TOKEN` |
| `gitlab.example.com` | `GITLAB_TOKEN_GITLAB_EXAMPLE_COM` (upper-case host, every other character becomes `_`) |

If the variable is not set, `glab config get token --host HOST` is tried as a
fallback, so `glab auth login` works too. A GitLab.com token is never reused for
another host.

```sh
export GITLAB_TOKEN=glpat-...
export GITLAB_TOKEN_GITLAB_EXAMPLE_COM=glpat-...
./dist/pr-inbox --gitlab gitlab.example.com
```

Notes:

- `--org` applies to GitHub only; GitLab shows everything you can see.
- A self-hosted instance with a private CA is trusted through the standard
  `SSL_CERT_FILE` / `SSL_CERT_DIR` environment variables; certificate checks are never disabled.
- Pipeline status needs one extra request per merge request, so GitLab queues with
  many items load more slowly than GitHub ones.
- With more than one provider, the UI shows a tab per instance (GitHub first; providers without
  credentials come last and show login instructions); each loads on first open. A single provider keeps the plain layout.
- `--demo` includes a sample GitLab instance, so you can try the tabs without credentials.

## Development

Building from source needs Go 1.27, Node.js 24.x with npm (see `.nvmrc`) for
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

### Verifying a release

`checksums.txt` is signed keylessly with [cosign](https://github.com/sigstore/cosign)
from the release workflow. Download the archive, `checksums.txt` and
`checksums.txt.sigstore.json` from the release, then:

```sh
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/cpanato/pr-inbox/\.github/workflows/release\.yaml@refs/tags/v.+$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

sha256sum --ignore-missing -c checksums.txt   # macOS: shasum -a 256 --ignore-missing -c checksums.txt
```

The first command proves the checksum file was produced by this repository's
release workflow; the second proves your archive matches it.
