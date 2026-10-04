FROM golang:1.25-alpine AS build

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /nudge-server ./cmd/server

FROM alpine:3.22

WORKDIR /app
RUN apk add --no-cache tzdata
COPY --from=build /nudge-server /app/nudge-server

EXPOSE 8080
CMD ["/app/nudge-server"]
