.PHONY: test check build setup db run

test:
	go test ./...
	node --test internal/dashboard/*test.cjs

check:
	go vet ./...
	go test -race ./...
	node --test internal/dashboard/*test.cjs

build:
	go build -o bin/nudge-server ./cmd/server

setup:
	python3 scripts/setup-env.py

db:
	docker compose up -d --wait postgres

# Environment must be exported first; Go does not automatically load .env.
run:
	go run ./cmd/server
