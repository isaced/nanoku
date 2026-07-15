GO ?= go
NPM ?= npm

ADMIN_USER ?= admin
ADMIN_PASSWORD ?= nanoku
LISTEN ?= :8080
DB ?= ./nanoku.db
CADDYFILE ?= ./Caddyfile

export NANOKU_ADMIN_USER := $(ADMIN_USER)
export NANOKU_ADMIN_PASSWORD := $(ADMIN_PASSWORD)
export NANOKU_LISTEN := $(LISTEN)
export NANOKU_DB := $(DB)
export NANOKU_CADDYFILE := $(CADDYFILE)

.PHONY: all dev build build-ui build-go run clean ent-generate

all: build

dev:
	@PIDS=$$(lsof -ti :3000 -i :8080 2>/dev/null); \
	if [ -n "$$PIDS" ]; then \
		echo "Killing stale dev processes on :3000, :8080: $$PIDS"; \
		echo "$$PIDS" | xargs kill 2>/dev/null || true; \
		sleep 1; \
	fi; \
	if [ -f .env ]; then \
		set -a; . ./.env; set +a; \
		echo "loaded .env"; \
	else \
		echo ".env not found — copy .env.example (NANOKU_SECRET_KEY is required)"; \
	fi; \
	rm -f /tmp/nanoku-dev-ui.pid /tmp/nanoku-dev-go.pid; \
	( cd ui && $(NPM) run dev ) > /tmp/nanoku-ui.log 2>&1 & echo $$! > /tmp/nanoku-dev-ui.pid; \
	$(GO) run . > /tmp/nanoku-go.log 2>&1 & echo $$! > /tmp/nanoku-dev-go.pid; \
	sleep 2; \
	echo ""; \
	echo "  UI  http://localhost:3000   (pid $$(cat /tmp/nanoku-dev-ui.pid),  log: /tmp/nanoku-ui.log)"; \
	echo "  Go  http://localhost:8080   (pid $$(cat /tmp/nanoku-dev-go.pid),  log: /tmp/nanoku-go.log)"; \
	echo ""; \
	trap 'kill $$(cat /tmp/nanoku-dev-ui.pid 2>/dev/null) $$(cat /tmp/nanoku-dev-go.pid 2>/dev/null) 2>/dev/null; rm -f /tmp/nanoku-dev-ui.pid /tmp/nanoku-dev-go.pid' INT TERM EXIT; \
	wait

dev-ui:
	cd ui && $(NPM) run dev

dev-go:
	$(GO) run .

build: build-ui build-go

build-ui:
	cd ui && $(NPM) run build

build-go:
	$(GO) build -o bin/nanoku .

run: build-go
	./bin/nanoku

ent-generate:
	cd internal/db && $(GO) run -mod=mod entgo.io/ent/cmd/ent@latest generate ./schema

clean:
	mavis-trash bin internal/api/dist