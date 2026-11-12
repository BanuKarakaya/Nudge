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
- Slack and Raindrop tokens currently lack application-layer encryption; restrict
  database access and encrypt storage/backups. Other credential categories are
  described in [the data inventory](docs/architecture.md).
- Update Go, container base images, Codex and dependencies after compatibility checks.
- Revoke a leaked credential immediately at its provider. Deleting it from a
  current file or rewriting Git history does not invalidate the leaked value.

Automated tests and scanning reduce risk but are not a penetration test or a
guarantee that all vulnerabilities or secrets have been found.
