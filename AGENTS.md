# AGENTS.md

Nanoku is an ultra-lightweight self-hosted deployment hub: a single Go binary that ships with an embedded React admin UI, pulls pre-built container images, and manages a Caddy reverse proxy for HTTPS. Think of it as a nano Coolify/Dokploy — "push → deployed" with minimal surface area.

## Setup commands

- Install deps (Go): `go mod download` (the `go.mod` is enough; no `go install` step). Requires **Go 1.26+** (`go.mod` pins `go 1.26.4`).
- Install deps (UI): `cd ui && npm ci` (Node 26 is what CI uses; `package-lock.json` is the source of truth).
- Start dev (Go + UI in parallel): `make dev` — also kills any stale process on :3000 / :8080 first.
- Start dev (Go only): `go run .`
- Start dev (UI only): `cd ui && npm run dev`
- Build (Go + UI): `make build` → produces `bin/nanoku`
- Build (UI embed only): `cd ui && npm run build` (output goes to `internal/api/dist`, embedded via `go:embed`)
- Test (Go): `go test -race -count=1 ./...`
- Test (UI): `cd ui && npm test`
- Lint / vet: `go vet ./...`
- Regenerate ent schema: `make ent-generate` (after editing files under `internal/db/schema/`)

> **Local `go test` will fail to compile unless `internal/api/dist` exists** — `internal/api/ui.go` `go:embed`s it. Always run `cd ui && npm run build` (or `make build-ui`) first. If you `go run .` with an empty/missing dist, the binary builds but serves no UI with no warning — prefer `make dev`.

## Project layout

- `main.go` — entry point: config load, DB open + migrate, Caddy container ensure, session store + admin seed, HTTP server wiring, graceful shutdown.
- `internal/api/` — HTTP handlers, routing, session auth, deploy lock, embedded UI.
- `internal/db/` — ent ORM schema + generated code. Business tables: `app`, `container`, `deploy`, `envvar`, `site`, `user`, `session`, `volume`. (`internal/db/hook/` is the ent lifecycle-hook package, not a table.) `db.go` opens the SQLite DB.
- `internal/caddy/` — Caddyfile writer/renderer (`Render`, atomic `WriteAtomic`).
- `internal/config/` — env-based config loading (`NANOKU_*` env vars; flags for `--listen`/`--db`/`--caddyfile`/`--skip-caddy-reload`).
- `internal/docker/` — Docker manager: Caddy container lifecycle, image pulls, container stats, log streaming, and `docker compose` / `docker login` via shelling out to the CLI.
- `ui/` — React 19 + Vite 8 + TanStack Router + Ant Design + Tailwind 4; builds into `internal/api/dist`. SPA auth is in-memory flag reconciled against `/api/me` on route entry (`ui/src/lib/auth.ts`).
- `composes/` — example compose apps used by the Docker deploy path.
- `docs/` — design docs (PLAN-v0, SCHEMA-v0, PROPOSAL-webhook, REVIEW-v0) — context, not user docs.

## Auth model

- **Admin UI** (`/api/*` except login/logout/trigger) sits behind **session-cookie auth** (`SessionAuth` in `internal/api/auth.go`), not Basic Auth. `POST /api/login` issues an HttpOnly, `SameSite=Strict`, `Secure`(when TLS) cookie; `GET /api/me` is the heartbeat the SPA checks on route entry.
- Sessions are stored hashed (SHA-256) in the `sessions` table; the raw token only ever lives in the cookie. TTL 7 days, sliding renewal.
- The HTTP trigger endpoint (`POST /api/apps/{name}/trigger`) accepts a per-app **Bearer token only** — no session, no HMAC. Compare tokens with `crypto/subtle.ConstantTimeCompare` (see `internal/api/trigger.go`).
- Login is rate-limited per IP (`AllowLogin`, 5/min) — note this trusts `X-Forwarded-For`, so it must sit behind a trusted proxy.

## Code style

- Go: standard `gofmt`, packages live under `internal/`, methods on receivers stay short; one `package` per top-level `internal/<name>` directory.
- The HTTP router uses Go 1.22+ pattern syntax (`mux.HandleFunc("GET /api/sites", …)`) — keep methods explicit and paths REST-shaped. Read path params with `pathID` / `pathIntID`.
- React/TS: strict mode (`tsconfig.json: strict: true`), Prettier default, single quotes.
- Use `crypto/subtle.ConstantTimeCompare` for any secret comparison.
- Generated code under `internal/db/*_query.go`, `*_create.go`, `*_update.go`, `*_delete.go`, `client.go`, `ent.go`, `runtime.go`, `mutation.go`, `tx.go`, and everything under `internal/db/{<entity>}/`, `internal/db/migrate/` — never hand-edit. Edit `internal/db/schema/*.go` and run `make ent-generate`.

## Testing instructions

- Go: `go test -race -count=1 ./...` (CI uses this exact invocation). **Build the UI first** (see Setup note) or it won't compile.
- UI: `cd ui && npm test` (Vitest + Testing Library + jsdom).
- New behavior → add a test in the same package: `*_test.go` next to the file it covers. Mirror existing patterns (`apps.go` → split tests, `trigger.go` → `trigger_test.go`, `caddyfile.go` → `caddyfile_test.go`).
- All tests must pass before opening a PR.

## PR & commit conventions

- Branch from `main`; never push to `main` directly.
- Commit messages: Conventional Commits — `feat:`, `fix:`, `refactor:`, `docs:`, `test:`, `chore:`, `ci:`. GoReleaser groups `feat` → Features, `fix` → Bug fixes, drops `docs/test/ci/chore/Merge*` from the changelog.
- Release: tag `v*` → `release.yml` runs GoReleaser, publishes archives (linux/darwin/windows × amd64/arm64, windows/arm64 excluded) and a sha256 checksums file.
- Open PR via `gh pr create` once `test.yml` is green.

## Security notes

- Never commit secrets. `.env` is in `.gitignore`; copy `.env.example` and set `NANOKU_ADMIN_PASSWORD` locally.
- `NANOKU_ADMIN_USER` / `NANOKU_ADMIN_PASSWORD` seed the first admin **only on a fresh DB**; once any user exists they are ignored (change passwords via the UI: `POST /api/me/password`).
- Registry credentials (`registry_password`) and trigger tokens are marked `Sensitive()` in the ent schema and never returned on List/Get, but they are stored **plaintext at rest** (schema has a `TODO: encrypt at rest`). Don't assume encryption.
- `registry_password` is passed to `docker login --password-stdin` (never via argv); the worker logs out after the pull.
- The Docker manager talks to `/var/run/docker.sock`; treat the host as trusted.
- CORS allows `Origin: *` (the trigger endpoint needs it for cross-origin CI calls); session cookies are `SameSite=Strict` so they won't ride along cross-site.
