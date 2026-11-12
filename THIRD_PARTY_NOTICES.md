# Third-party notices

The root [MIT license](LICENSE) covers original Nudgy code, documentation and
maintainer-created artwork. It does not replace licenses of third-party software,
external services or third-party trademarks.

## Go server

This inventory is based on the modules actually linked by `./cmd/server`.
Exact versions and checksums are in `go.mod` and `go.sum`. License texts were
copied from those downloaded module versions into `LICENSES/`, including the
CoreOS NOTICE. Distribute these files alongside a compiled server.

| Module | Version | License |
| --- | --- | --- |
| `github.com/coreos/go-oidc/v3` | v3.17.0 | Apache-2.0; includes NOTICE |
| `github.com/go-jose/go-jose/v4` | v4.1.4 | Apache-2.0 |
| `github.com/jackc/pgx/v5` | v5.9.2 | MIT |
| `github.com/jackc/pgpassfile` | v1.0.0 | MIT |
| `github.com/jackc/pgservicefile` | v0.0.0-20240606120523-5a60cdf6a761 | MIT |
| `github.com/jackc/puddle/v2` | v2.2.2 | MIT |
| `golang.org/x/oauth2` | v0.28.0 | BSD-3-Clause |
| `golang.org/x/sync` | v0.25.0 | BSD-3-Clause |
| `golang.org/x/text` | v0.44.0 | BSD-3-Clause |
| Go runtime / standard library | Build toolchain in `go.mod` / `Dockerfile` | BSD-3-Clause; see `LICENSES/Go-LICENSE.txt` |

The production Docker image includes this notice, the project license and the
bundled Go dependency notices under `/usr/share/doc/nudgy/`. Go packages used
only by tests or development tools are not part of the compiled server; their
own module licenses still apply when redistributing those tools.

## Runtime images and tools

- [Codex](https://github.com/openai/codex) is installed separately as
  `@openai/codex@0.164.0` by the Dockerfile. Its upstream license is
  [Apache-2.0](https://github.com/openai/codex/blob/main/LICENSE). Preserve the
  package's own license and bundled third-party notices when redistributing it.
- The runtime uses the Node.js Debian image, Git and OS packages. Their license
  files remain in their upstream package locations (including `/usr/share/doc`).
- PostgreSQL runs in a separate container and retains its
  [PostgreSQL license](https://www.postgresql.org/about/licence/).
- The optional landing-page container uses nginx and its upstream license.

This document inventories the application dependencies, not every package in
every base image. Redistributing a complete container also requires preserving
that image's package notices and satisfying any applicable source obligations.
Recheck the inventory when changing build inputs.

## Images and fonts

The maintainer confirmed that the Nudgy mascot images are their own/generated
artwork and authorized their inclusion under MIT. This covers
`internal/dashboard/static/nudgy-mascot.webp`,
`railway/nudgy-tanitim/public/nudgy.webp`, and the embedded copy in the landing-page
function. There are no separate paid asset files required by the application.

The landing page requests DM Sans and Manrope through Google Fonts; font binaries
are not committed here. Their upstream distributions use the SIL Open Font
License: [DM Sans](https://github.com/google/fonts/tree/main/ofl/dmsans),
[Manrope](https://github.com/google/fonts/tree/main/ofl/manrope). If you bundle or
self-host those fonts, include their OFL license files. A system-font fallback is
already configured.

Slack, GitHub, GitLab, Jira, Trello, Feedbin, Raindrop, Google, OpenAI, OpenRouter
and Railway names identify integrations. This project does not grant rights to
their names, trademarks, service accounts or API access.
