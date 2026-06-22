# AGENTS.md

Nanoku is an ultra-lightweight self-hosted deployment hub: a single Go binary that ships with an embedded React admin UI, pulls pre-built container images, and manages a Caddy reverse proxy for HTTPS. Think of it as a nano Coolify/Dokploy — "push → deployed" with minimal surface area.

## Setup commands

- Install deps (Go): `go mod download` (the `go.mod` is enough; no `go install` step)
- Install deps (UI): `cd ui && npm ci`
- Start dev (Go + UI in parallel): `make dev`
- Start dev (Go only): `go run .`
- Start dev (UI only): `cd ui && npm run dev`
- Build (Go + UI): `make build` → produces `bin/nanoku`
- Build (UI embed only): `cd ui && npm run build` (output goes to `internal/api/dist`, embedded via `go:embed`)
- Test (Go): `go test -race -count=1 ./...`
- Test (UI): `cd ui && npm test`
- Lint / vet: `go vet ./...`
- Regenerate ent schema: `make ent-generate` (after editing files under `internal/db/schema/`)

> CI builds the UI **before** `go test` because `internal/api/ui.go` `go:embed`s `internal/api/dist`. Don't skip the UI build step in CI or local tests.

## Project layout

- `main.go` — entry point: config load, DB open, Caddy container ensure, HTTP server wiring
- `internal/api/` — HTTP handlers, routing, auth, deploy lock, embedded UI
- `internal/db/` — ent ORM schema + generated code (`app`, `container`, `deploy`, `envvar`, `site`, `hook`); `db.go` opens the SQLite DB
- `internal/caddy/` — Caddyfile writer/renderer
- `internal/config/` — env-based config loading (NANOKU_* env vars)
- `internal/docker/` — Docker manager (Caddy container lifecycle, image pulls, log streaming)
- `ui/` — React 19 + Vite 8 + TanStack Router + Ant Design + Tailwind 4; builds into `internal/api/dist`
- `composes/` — example compose apps used by the Docker deploy path
- `docs/` — design docs (PLAN-v0, SCHEMA-v0, PROPOSAL-webhook) — context, not user docs

## Code style

- Go: standard `gofmt`, packages live under `internal/`, methods on receivers stay short; one `package` per top-level `internal/<name>` directory.
- The HTTP router uses Go 1.22+ pattern syntax (`mux.HandleFunc("GET /api/sites", …)`) — keep methods explicit and paths REST-shaped.
- React/TS: strict mode (`tsconfig.json: strict: true`), Prettier default, single quotes.
- Use `crypto/subtle.ConstantTimeCompare` for any secret comparison (see `internal/api/trigger.go` for the pattern).
- Generated code under `internal/db/*_query.go`, `*_create.go`, `*_update.go`, `*_delete.go`, `client.go` — never hand-edit. Edit `internal/db/schema/*.go` and run `make ent-generate`.

## Testing instructions

- Go: `go test -race -count=1 ./...` (CI uses this exact invocation).
- UI: `cd ui && npm test` (Vitest + Testing Library + jsdom).
- New behavior → add a test in the same package: `*_test.go` next to the file it covers. Mirror existing patterns (`apps.go` → split tests, `trigger.go` → `trigger_test.go`, `caddyfile.go` → `caddyfile_test.go`).
- All tests must pass before opening a PR.

## PR & commit conventions

- Branch from `main`; never push to `main` directly.
- Commit messages: Conventional Commits — `feat:`, `fix:`, `refactor:`, `docs:`, `test:`, `chore:`, `ci:`. GoReleaser groups `feat` → Features, `fix` → Bug fixes, drops `docs/test/ci/chore/Merge*` from the changelog.
- Release: tag `v*` → `release.yml` runs GoReleaser, publishes archives (linux/darwin/windows × amd64/arm64) and a sha256 checksums file.
- Open PR via `gh pr create` once `test.yml` is green.

## Security

- Never commit secrets. `.env` is in `.gitignore`; copy `.env.example` and set `NANOKU_ADMIN_PASSWORD` locally.
- `NANOKU_ADMIN_PASSWORD` is required at startup (`internal/config/config.go` rejects empty).
- The HTTP trigger endpoint (`POST /api/apps/{name}/trigger`) accepts a per-app bearer token only — no HMAC, no signature. Compare tokens with `crypto/subtle.ConstantTimeCompare`.
- Admin UI (`/api/*` except the trigger) sits behind HTTP Basic Auth — do not weaken this.
- The Docker manager talks to `/var/run/docker.sock`; treat the host as trusted.
