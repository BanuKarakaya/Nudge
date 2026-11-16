# Changelog

## Unreleased

### Fixed
- Explicit action requests execute without redundant confirmation by default; existing manager approval/disabled policies remain enforced. Successful tracker actions return concise links and human-readable results.
- Personal data, reminders, tool policies and audit history use the shared dashboard layout, navigation and styled forms; audit JSON remains available as an explicit download.
- Natural dashboard requests send the requester's private sign-in link; setup guidance respects user roles, negation and actual installation capabilities.
- Personal Feedbin/Raindrop accounts can be renewed and disconnected without workspace scope activation; existing Feedbin credentials no longer block renewal.
- Existing conversations refresh their native tool catalog when custom tools change, preserving delivered history.
- Provider text envelopes are rendered as readable Slack messages instead of raw JSON.
- Binary release bundles include third-party notices and dependency licenses, covered by SHA-256 checksums.
- Patched the runtime zlib package for CVE-2026-4738; corrected outdated token-encryption and OAuth-state documentation.
- Workspace invitations and shared-scope Jira/Trello writes work in production wiring.
- Provider authorization requires the link owner's live panel session throughout redirects.
- Edit leases release database connections before Git/network operations. Failed clones no longer consume open-task capacity.
- Rotated OAuth credentials survive later identity-query failures; Raindrop refreshes and paginates.
- Merge evidence excludes assistant messages and binds repository/number together.
- Queue retries terminate; Slack uninstall/revocation disables the installation and pending work.
- Repository listings use small metadata and pushed tasks release full snapshots.
- Slack output cannot generate angle-bracket mentions or hide a different link destination.
- Updated go-jose to 4.1.6; moved the runtime to Alpine and removed build-only package managers. The verified arm64 image has no HIGH/CRITICAL Trivy findings.

### Added
- Opt-in real-model language acceptance tests with bounded inference requests and synthetic tool effects.
- Concurrent PostgreSQL queue/retry and connection-loss tests, native provider outage recovery, and browser-backed member/manager installation isolation.
- Live dashboard request stages, tool failures, delivery status, model/token usage and role-scoped team/user metrics.
- Custom HTTPS MCP integrations with selected tools, shared service-account credentials and approval policies.
- Installation-aware workspace guide for account setup, model settings, permissions, privacy and supported product limits.
- Standalone Compose setup for external PostgreSQL and configurable local ports.
- Real PostgreSQL/browser installation isolation test and configuration-generation checks.
- Optional human approval for external write tools, workspace tool policies and revision-bound push/merge approvals.
- Context-bound encrypted secrets, key IDs and keyring rotation; migration of legacy Slack/Raindrop plaintext tokens.
- Versioned migrations, retention, personal export/deletion, configuration audit history and protected metrics.
- Daily user/workspace token reservations, workspace concurrency limits and OAuth request limits.
- Workspace timezones, reminder settings and durable catch-up scheduling.
- Operator-configured Responses-compatible AI endpoint, including loopback servers.
- Pinned native harness tests, race tests, formatting, static analysis and image vulnerability scanning in CI.

### Upgrade
Read [operations](docs/operations.md) and [review remediation](docs/review-remediation.md).
Tools without an explicit policy now default to automatic execution of user requests. Existing explicit approval/disabled policies are preserved; managers can require approval at `/dashboard/actions`. Existing single-workspace reminder configuration is adopted once.
Back up the database and encryption keys before starting the upgraded server.
