# Security Policy

## Supported versions

Only the latest release receives security fixes.

## Reporting a vulnerability

Please do not open a public issue for security problems. Report them privately
through GitHub's
[private vulnerability reporting](https://github.com/cpanato/pr-inbox/security/advisories/new).

Include the affected version, steps to reproduce, and the impact you expect.
You can expect an initial response within a few days.

## Scope

PR Inbox runs locally and reuses your GitHub CLI credentials. Reports about
the following are especially welcome:

- Exposure of the GitHub token or the launch secret outside the local process.
- Bypasses of the loopback-only binding, Origin/Host validation, session cookie,
  or CSRF protection.
- Cross-site scripting or content security policy bypasses in the embedded UI.
- Supply chain issues in the release workflow or published artifacts.
