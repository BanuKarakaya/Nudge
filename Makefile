.PHONY: test check test-installation check-integration build setup db run fmt-check lint eval-language

test: test-installation
	go test ./...
	node --test internal/dashboard/*test.cjs

check: fmt-check test-installation
	go vet ./...
	go test -race -count=1 ./...
	node --test internal/dashboard/*test.cjs

test-installation:
	python3 scripts/test-installation.py

# Fail instead of silently skipping database/native tests during release review.
check-integration:
	@test -n "$$NUDGE_TEST_DATABASE_URL" || (echo "Set NUDGE_TEST_DATABASE_URL to a disposable database"; exit 1)
	@test -n "$$NUDGY_TEST_CODEX_BINARY" && test -x "$$NUDGY_TEST_CODEX_BINARY" || (echo "Set NUDGY_TEST_CODEX_BINARY to the pinned executable"; exit 1)
	$(MAKE) check

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

# Paid inference; requires NUDGY_EVAL_MODELS, OPENROUTER_API_KEY and pinned binary.
# Synthetic tool effects only; see docs/natural-language-flows.md.
eval-language:
	NUDGY_LIVE_LANGUAGE_TEST=1 go test -count=1 -timeout=15m -v ./internal/codex -run '^TestLiveNaturalLanguageFlows$$'
