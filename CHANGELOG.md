# Changelog

## Unreleased

### Fixed
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
- Default human approval for external write tools, workspace tool policies and revision-bound push/merge approvals.
- Context-bound encrypted secrets, key IDs and keyring rotation; migration of legacy Slack/Raindrop plaintext tokens.
- Versioned migrations, retention, personal export/deletion, configuration audit history and protected metrics.
- Daily user/workspace token reservations, workspace concurrency limits and OAuth request limits.
- Workspace timezones, reminder settings and durable catch-up scheduling.
- Operator-configured Responses-compatible AI endpoint, including loopback servers.
- Pinned native harness tests, race tests, formatting, static analysis and image vulnerability scanning in CI.

### Upgrade
Read [operations](docs/operations.md) and [review remediation](docs/review-remediation.md).
Existing tool policies default to human approval. Existing single-workspace reminder configuration is adopted once.
Back up the database and encryption keys before starting the upgraded server.
