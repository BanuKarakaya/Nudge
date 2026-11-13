FROM golang:1.27.0-alpine AS build

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN --mount=type=tmpfs,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -o /nudge-server ./cmd/server

FROM node:22-alpine AS runtime

WORKDIR /app
ARG CODEX_VERSION=0.164.0
RUN apk add --no-cache ca-certificates tzdata git tini \
    && npm install --global --ignore-scripts --no-audit --no-fund @openai/codex@${CODEX_VERSION} \
    && npm cache clean --force \
    && rm -rf /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/corepack \
    && rm -rf /opt/yarn-v* \
    && rm -f /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/corepack /usr/local/bin/yarn /usr/local/bin/yarnpkg /usr/local/bin/pnpm /usr/local/bin/pnpx \
    && codex --version \
    && mkdir -p /var/lib/nudgy/codex \
    && chown -R node:node /var/lib/nudgy
COPY --from=build /nudge-server /app/nudge-server
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/doc/nudgy/
COPY LICENSES/ /usr/share/doc/nudgy/LICENSES/

ENV NUDGY_CODEX_STATE_DIR=/var/lib/nudgy/codex
USER node

EXPOSE 8080
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["/app/nudge-server"]
