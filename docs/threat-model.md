# Nudgy threat model

## Protected assets and trust boundaries

Provider credentials, workspace AI balances, private repository content, Slack
conversations, issue/card state and manager configuration are protected assets.
Slack signatures and panel sessions establish identity. Tool arguments, model
responses, repository files, RSS articles, search results, tracker descriptions
and other thread participants are untrusted. The model never chooses a user ID,
workspace identity, secret destination or authorization policy.

A workspace manager chooses shared resource scope and policy; this does not grant
members the manager's provider privileges. Each user authorizes their own account.
Live provider access and shared scope are checked again before operations. Provider
connection links require the owner's live panel session and bind the callback to
that session. OAuth state and consumed links are persisted and expire.

## Write authorization

Read tools expose only allowed resources. Private draft edits cannot run shell,
hooks or repository tests. External writes require a persisted, owner-bound Slack
approval by default. Exact arguments are displayed as plain text. Push approvals
freeze file digests; merge approvals freeze source commit/destination. Permission
and branch protection checks still run at execution. Policy can disable a tool;
`auto` is an explicit manager choice that allows model-directed calls.

Merge intent uses only user-authored evidence, and repository/PR identifiers must
be grounded together. Assistant text cannot grant authorization. The host treats
thread context and tool descriptions/results as data. These instructions improve
model behavior but are not a security boundary; permissions and approval enforce
that boundary even if the model follows malicious content.

Automation entrypoints across providers are blocked, including common CI trees,
Makefiles, shell scripts, package manifests and container definitions. Arbitrary
source can still be executed by an existing CI pipeline: no filename list proves
that a push cannot execute code. Human review and isolated CI credentials remain
necessary for untrusted generated source.

Slack output strips active mentions/directives and exposes URL targets rather
than trusting model-supplied labels. Approval arguments are plain text. Browser
templates escape values and authenticated mutations require CSRF/origin checks.

## Storage and availability

AES-GCM ciphertext binds table and identity/task context, carries a key ID and
supports retained decryption keys. Legacy ciphertext can be read for migration;
old backups require separate retention controls. Infrastructure operators can
read process memory and decrypted content. This is not end-to-end encryption.

Short fenced edit leases avoid holding pooled database connections during Git
operations. Failed clones are terminal. Snapshot lists read metadata; full source
is removed after push. Queues have bounded attempts and durable output checkpoints.
HTTP timeouts, OAuth admission limits, token reservations and workspace concurrency
bound ordinary misuse. Provider outages and billing spikes still need monitoring.

Remote effects and database receipts cannot share a transaction. A process crash
can leave an approved action's result unknown; Nudgy refuses to execute it again
without a fresh user request. Slack delivery can duplicate in the acceptance/save
window. See [operations](operations.md) for recovery and retention behavior.

## Validation and boundaries

Regression tests cover real PostgreSQL onboarding, tenant-scoped tracker writes,
owner/session-bound connection flows (including browser cookie paths), single-slot
pool edit operations, credential refresh failures, terminal retries, single-use
approval, revision changes, encryption AAD, output sanitization and native tool
isolation. These checks do not constitute a live penetration test.

Compiled-in tool modules are trusted server code. Registry defaults new extension
operations to approval and prevents name collisions; module implementations must
still validate input and live provider access. Remote MCP/plugin discovery is not
enabled. No target repository code is executed by Nudgy. Enterprise provider
protocols and unrestricted plugin execution require separate threat review.
