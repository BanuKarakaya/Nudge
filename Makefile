.PHONY: test check build setup db run fmt-check lint

test:
	go test ./...
	node --test internal/dashboard/*test.cjs

check: fmt-check
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

fmt-check:
	@test -z "$$(gofmt -l $$(git ls-files '*.go') $$(git ls-files --others --exclude-standard '*.go'))" || (echo "Run gofmt on Go sources"; exit 1)

lint:
	go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
