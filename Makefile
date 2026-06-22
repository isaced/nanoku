GO ?= go
PNPM ?= pnpm

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
	$(MAKE) -j2 dev-ui dev-go

dev-ui:
	cd ui && $(PNPM) dev

dev-go:
	$(GO) run .

build: build-ui build-go

build-ui:
	cd ui && $(PNPM) build

build-go:
	$(GO) build -o bin/nanoku .

run: build-go
	./bin/nanoku

ent-generate:
	cd internal/db && $(GO) run -mod=mod entgo.io/ent/cmd/ent@latest generate ./schema

clean:
	mavis-trash bin internal/api/dist