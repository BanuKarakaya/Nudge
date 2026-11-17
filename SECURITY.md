# Security policy

Security fixes target the current `main` branch. There are no separately
maintained release lines or guaranteed response-time commitments yet.

## Report a vulnerability privately

Use GitHub's **Security → Report a vulnerability** for
[BanuKarakaya/Nudge](https://github.com/BanuKarakaya/Nudge/security).
If the repository does not offer that button, request a private reporting channel
from the maintainer via their GitHub profile. A public issue may ask for a private
channel, but must not contain exploit details, credentials or affected user data.

Include the affected commit, impact, minimal reproduction and a proposed fix if
available. Test only against your own installation and synthetic accounts. Do not
send real API keys or private customer content with the report.

## Operator notes

- Keep `.env`, encryption keys, PostgreSQL backups and Codex state private.
- Use HTTPS, private database networking and independent stable encryption keys.
- Slack and Raindrop tokens are encrypted by the production server; legacy
  plaintext records are migrated on startup. Restrict database access and encrypt
  storage/backups as well. Other credential categories are
  described in [the data inventory](docs/architecture.md).
- Update Go, container base images, Codex and dependencies after compatibility checks.
- Revoke a leaked credential immediately at its provider. Deleting it from a
  current file or rewriting Git history does not invalidate the leaked value.

Automated tests and scanning reduce risk but are not a penetration test or a
guarantee that all vulnerabilities or secrets have been found.

## Remote MCP connections

Workspace managers can explicitly share selected MCP tools through a dedicated
service-account Bearer token. This does not provide employee-level authorization
in the external application. Connector tokens use encrypted, team/connection/revision-bound
storage. New operations default to human approval, including tools advertised as
read-only. Endpoint/credential/catalog changes invalidate old approval tool names.
The HTTP client checks DNS addresses at dial time, disables redirects and proxies,
and limits requests/results. Private networks require operator-approved origins.
See [MCP security and deployment details](docs/mcp-connections.md).

## Live dashboard data

Workspace managers can view their own team's request metadata, model/token totals,
Slack display names and usage ranking. Members can view only their own operational
records. Live polling uses the existing session and rechecks membership; it does
not accept operator keys or caller-selected identity. Operational events contain
host-selected stages, safe error codes and tool/model identifiers, not conversation
bodies, tool arguments, credentials or raw provider errors. Historical raw message
columns and queued payload/results are excluded from normal dashboard list polling.
Selecting an owned job exposes the requester’s message/reply and separately
encrypted, redacted provider request/response content. Only the requesting member
can read content, including when the viewer is a manager. Wire capture is bounded
and never stores authentication headers. Secret redaction is best effort; ordinary
private prose is still private data protected by ownership and encryption.
Personal export includes live metadata; personal deletion and job retention delete
its associated per-model usage, encrypted model calls and signed-in HTTP error logs. This is application access control, not protection
against infrastructure administrators with direct database access.
