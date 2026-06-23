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
	fi
	$(MAKE) -j2 dev-ui dev-go

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