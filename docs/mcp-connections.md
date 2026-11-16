# Connect an internal application with MCP

Nudgy can act as an MCP client for remote applications. A company does not need
to fork or rebuild Nudgy to add another provider: a workspace manager adds its
MCP endpoint in **Dashboard → Integrations → Özel bağlantıları yönet**.

An application that already exposes MCP needs configuration only. An application
with only a REST API needs an MCP adapter on the application side. OpenAPI/Swagger
import is not implemented. Existing GitHub, GitLab, Jira, Trello, Feedbin and
Raindrop integrations continue to work independently.

## Supported connections

- HTTPS **Streamable HTTP**, using the official MCP Go SDK v1.7.0. Protocol
  negotiation supports the SDK's protocol versions, including 2026-07-28 and
  earlier Streamable HTTP versions.
- A dedicated **Bearer service-account token**, entered by the manager and
  encrypted using the workspace AI encryption key, with team/connection/revision
  binding. Tokens are never sent to the model, returned in the dashboard or
  included in connector error messages.
- Tool discovery, manager-selected tools, object input schemas, text and
  structured JSON results. The host validates arguments before calling the tool.
- No subprocess/stdio servers, legacy standalone SSE endpoints, OAuth login,
  per-employee external credentials, sampling, elicitation or resource fetching.
  Servers that require a follow-up input interaction return an error; Nudgy does
  not automatically fulfill it or repeat the operation.

The [official SDK documentation](https://github.com/modelcontextprotocol/go-sdk)
describes MCP server implementations and supported protocol versions.

## Company setup

1. Deploy the application's MCP server behind HTTPS. The endpoint must be
   reachable from the Nudgy server, not only from an employee's browser.
2. Create a dedicated token with only the permissions needed by the team. **Do
   not reuse a Slack bot token, AI key or a broad personal administrator token.**
3. Open the private connections page, enter a name, full MCP endpoint (such as
   `https://tools.example.com/mcp`) and token, then choose **Bağlan ve araçları
   getir**. This only discovers tools; it does not enable or call them.
4. Review the returned tool descriptions. Select at most 12 tools, enable the
   connection and explicitly acknowledge sharing the service-account permissions
   with all Nudgy users in this Slack workspace. Save.
5. Ask Nudgy in Slack to use that application. Selected tools are included in
   the current user's workspace tool catalog. Enabled MCP tools execute explicit user requests
   without a second confirmation by default. Managers can require approval or
   disable individual tools in **Araç izinleri**. Even tools labeled read-only
   by the server pass through that policy. Automatic mode does not make a
   remote operation idempotent.

**Access model:** this first version is a shared service connection. It does not
intersect permissions with each employee's personal account in the external
application. Only expose data and operations all intended Nudgy users may access.
If the application needs per-user permission checks, deploy an adapter with the
appropriate authorization design or wait for per-user authentication support;
do not assume a Slack identity is automatically an external application identity.
Nudgy does not forward Slack/AI credentials or trust model-supplied user IDs as
authorization. MCP results are visible in the requesting Slack conversation and
may be processed by that workspace's selected AI provider.

## Private networks and self-hosting

Public HTTPS destinations work without an additional environment variable.
Private IP ranges are blocked by default. For an intranet MCP server, the **Nudgy
deployment operator** can allow its exact HTTPS origin:

```dotenv
NUDGY_MCP_PRIVATE_ORIGINS=https://tools.corp.example,https://tickets.corp.example:8443
```

This is a comma-separated origin list, not a list of paths or wildcards. Configure
it on the Nudgy Railway service or in the self-hosted app environment and restart.
The operator must also provide network routing and trusted TLS certificates.
Railway cannot reach a company's private network solely because this variable is
set; a reachable HTTPS gateway or private network connection is still required.
Loopback, link-local, known metadata and special-purpose IPs remain blocked even
for allowed origins. A reverse proxy with a normal internal address and valid TLS
certificate can expose a local application safely. HTTPS verification is never
disabled. HTTP proxies and redirects are not used; DNS answers are checked at
connection time and the transport dials the checked address.

## Updates, revocation and limits

- Up to five connections per workspace, 32 discoverable tools per server and 12
  selected tools per connection. Expose a smaller MCP catalog if necessary.
- Discovery has a 30-second deadline; calls have a 45-second deadline. Each HTTP
  response is limited to 512 KiB, each result to 64 KiB and input to 16,000 bytes.
  Tool schemas/descriptions are bounded; external JSON Schema references are
  rejected. Supported input validation drafts are draft-07 and 2020-12.
- Configuration changes create new tool names. Old queued approvals and
  explicit tool policies no longer apply; newly enabled tools use the default auto policy. Tool/model routes must be reselected
  after configuration changes if needed.
- Refreshing the server or credentials disables the connection and clears tool
  selections. Review the new catalog and enable it again. A changed endpoint
  requires a new token; an existing token is never forwarded to the new address.
- Immediately before a call, Nudgy checks current workspace access and connection
  revision, then compares the live tool definition to the saved definition.
  Changed tools fail closed until a manager refreshes and reviews them. This
  cannot detect a server changing its implementation without changing its schema:
  the company still needs to trust and operate its MCP server.
- Connection saves and deletions record the acting user and configuration revision
  in the workspace audit log without tokens or tool arguments.
- Closing or deleting a connection prevents subsequent calls, including pending
  approvals. A request already dispatched to the remote server cannot be undone.
  Uninstalling Nudgy removes that workspace's MCP connections and credentials.
- The client does not reconnect/retry tool calls automatically. After a timeout,
  check the external application before requesting the operation again. Generic
  remote tools cannot promise exactly-once side effects; adapters should implement
  their own idempotency rules for writes.

## Runnable adapter example

[`examples/mcp-company/main.go`](../examples/mcp-company/main.go) is a small MCP
server with one read tool and synthetic tickets. It uses a separate token and
does not access real company data. Run it behind your HTTPS reverse proxy:

```sh
export COMPANY_MCP_TOKEN="$(openssl rand -hex 32)"
go run ./examples/mcp-company
```

It listens on `127.0.0.1:8090/mcp`. Configure the reverse proxy to publish it at a
reachable HTTPS URL, then enter that URL and `COMPANY_MCP_TOKEN` in the dashboard.
Replace the demo lookup with your application's API, checking the service
account's permissions in the adapter. Never accept a user/role field in model
arguments as proof of identity.

## Verification

Automated tests exercise the real MCP SDK over a local TLS server, invalid inputs,
tool-definition drift, remote errors, network restrictions, redirects and response
bounds. Service/dashboard tests cover manager authorization, CSRF, shared-account
acknowledgement, escaped descriptions, runtime registration, approval defaults and
revocation. PostgreSQL tests cover tenant isolation, encrypted credential binding,
concurrent updates and stale forms. Tests use synthetic data and no real company
application. A company's own MCP server still needs an acceptance test against
its actual authentication, network and tool behavior.

Local validation on 2026-11-15: the full Go race suite passed 333 top-level tests
(907 including subtests); the two explicit live-model tests were skipped. The
10 dashboard JavaScript tests, five installation configuration tests, staticcheck,
Go vet, native Codex tests and Go vulnerability scan passed. A fresh server
started with all 16 migrations and returned healthy HTTP responses. Follow-up
tests covered the final panel and connector audit-log edits.
