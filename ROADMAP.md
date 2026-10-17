# Roadmap

This roadmap records delivered capabilities and ideas to revisit. Backlog items
have no committed implementation order or delivery date. Provider coverage and
limits are documented in the [README](README.md).

## Delivered

- [x] Authenticated **To review** and authored queues, organization/repository
  filters, CI labels, bounded discovery, coverage notices and provider links.
- [x] GitHub, GitLab.com and self-hosted GitLab instances, with a separate UI tab
  per configured provider and a provider-neutral PR/MR model.
- [x] Review workspace with plain-text descriptions, author/branch/head details,
  paginated discussion timelines and links to the provider.
- [x] Diffs pinned to the inspected commit, including explicit handling of large,
  binary and truncated diffs, plus detailed checks and pipeline results.
- [x] Local file-viewed markers and review drafts tied to the inspected revision.
- [x] Overall GitHub reviews with confirmation, unchanged-head/account/open-PR
  checks, pending-review detection, durable operation tracking and reconciliation.
- [x] GitLab comments, formal approvals and requested-changes state, with explicit
  provider effects and protection against uncertain or partial outcomes.
- [x] Previously reviewed history from confirmed app reviews, changed-head
  detection, and explicit freshness and coverage limits.
- [x] Persistent snooze deadlines with overdue reminders after server or machine
  restarts, cancellation and rescheduling.
- [x] Filters, ordering and queue preferences saved per account and provider.
- [x] Separate GitHub direct/team review-request discovery, deduplicated reasons
  and completeness notices; explicit GitLab group approval eligibility exclusions.
- [x] Basic `doctor` checks for authentication, selected accounts and provider
  connectivity without starting the server.

## Ideas to revisit

These ideas are recorded for later discussion, without selecting the next task.

- [ ] **Expand diagnostics.** Build on the existing `doctor` command to check
  provider capability/permission errors and access to the configured state
  directory, with actionable messages that never print credentials.
- [ ] **Automatic inbox refresh.** Add a configurable interval, respect API rate
  limits and retry guidance, pause polling in hidden tabs, and clearly indicate
  stale results and the last successful refresh.
- [ ] **Local-state backup and restore.** Export and import drafts, viewed markers,
  review history, snoozes and preferences without credentials. Preserve account,
  provider and revision boundaries; define validation and conflict handling before
  implementing restore.

## Deferred: inline review

Kept as backlog reference only; no implementation is currently planned.

- [ ] Comments on diff lines, including renamed files.
- [ ] Local inline drafts tied to file, line and inspected commit.
- [ ] Confirmed submission of inline comments with an overall review.
- [ ] Thread replies and resolution where supported by the provider.
- [ ] Outdated-comment labels and position validation after head changes.
