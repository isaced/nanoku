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
	@PIDS=$$(lsof -ti :3000 -i :8080 2>/dev/null || true); \
	if [ -n "$$PIDS" ]; then \
		echo "Killing stale dev processes on :3000, :8080: $$PIDS"; \
		echo "$$PIDS" | xargs kill 2>/dev/null || true; \
		sleep 1; \
	fi; \
	if [ -f .env ]; then \
		set -a; . ./.env; set +a; \
		echo "loaded .env"; \
	else \
		echo ".env not found - copy .env.example (NANOKU_SECRET_KEY is required)"; \
	fi; \
	mkdir -p /tmp/nanoku-dev; \
	rm -f /tmp/nanoku-dev/ui.log /tmp/nanoku-dev/go.log /tmp/nanoku-dev/ui.pid /tmp/nanoku-dev/go.pid; \
	echo ""; \
	echo "  UI  http://localhost:3000   log: /tmp/nanoku-dev/ui.log"; \
	echo "  Go  http://localhost:8080   log: /tmp/nanoku-dev/go.log"; \
	echo "  (Ctrl-C to stop)"; \
	echo ""; \
	( cd ui && $(NPM) run dev ) 2>&1 | perl -pe 'BEGIN{$$|=1} print "\033[36m[ui]\033[0m "' | tee /tmp/nanoku-dev/ui.log & \
	UI_PID=$$!; \
	$(GO) run . 2>&1 | perl -pe 'BEGIN{$$|=1} print "\033[35m[go]\033[0m "' | tee /tmp/nanoku-dev/go.log & \
	GO_PID=$$!; \
	echo $$UI_PID > /tmp/nanoku-dev/ui.pid; \
	echo $$GO_PID > /tmp/nanoku-dev/go.pid; \
	sleep 2; \
	trap "kill $$UI_PID $$GO_PID 2>/dev/null; rm -f /tmp/nanoku-dev/ui.pid /tmp/nanoku-dev/go.pid" INT TERM EXIT; \
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
	cd internal/db && GOTOOLCHAIN=go1.26.4 $(GO) run -mod=mod entgo.io/ent/cmd/ent@latest generate ./schema

clean:
	mavis-trash bin internal/api/dist