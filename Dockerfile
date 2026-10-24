FROM golang:1.25-alpine AS build

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /nudge-server ./cmd/server

FROM node:22-bookworm-slim

WORKDIR /app
ARG CODEX_VERSION=0.164.0
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && npm install --global --ignore-scripts --no-audit --no-fund @openai/codex@${CODEX_VERSION} \
    && codex --version \
    && mkdir -p /var/lib/nudgy/codex \
    && chown -R node:node /var/lib/nudgy
COPY --from=build /nudge-server /app/nudge-server

ENV NUDGY_CODEX_STATE_DIR=/var/lib/nudgy/codex
USER node

EXPOSE 8080
CMD ["/app/nudge-server"]
