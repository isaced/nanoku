# Compose-mode Site Attach — Design Record

## Why

Before this change, a `docker compose` App in nanoku could not be
attached to a Site through any sane path. The legacy model assumed
"one app = one container = one port"; the deploy path recorded
whichever container came back first from `docker compose ps` (line
`names[0]`) and the Caddyfile render produced a literal
`nanoku-<app>-1:0` upstream — broken at the network layer.

This document records the schema, API, and UX decisions that landed
to fix it.

## Data model

Two new fields. No new table.

| Field | Where | Purpose |
|---|---|---|
| `App.exposed_ports` | `internal/db/schema/app.go` | JSON array of `{name, port}` — the user-declared set of compose services Caddy should be able to reach. Validated to be JSON-encodable, sorted, no duplicate service names. |
| `Site.app_service` | `internal/db/schema/site.go` | The service within a compose app the site proxies to. Empty for docker-mode apps and for free-upstream sites. |

`App.exposed_ports` is a `field.Text` (nullable). `Site.app_service`
is `field.String` (nullable, max 64 chars). Both are populated by
the editor and validated server-side; the editor strips incomplete
rows before submit so a half-typed "add row" doesn't bounce.

`App.exposed_ports` is round-tripped through a small JSON helper in
`internal/api/exposed_ports.go` (`ParseExposedPorts` / `MarshalExposedPorts`).
The wire format is the literal JSON array; no ent typed-JSON to keep
the column easy to inspect from `sqlite3`.

## Upstream resolution

`internal/api/handlers.go:computeUpstreamForApp` is the single source
of truth. It returns:

- **docker app** with a `current_container` row:
  `<container.name>:<app.port>`
- **docker app** without a `current_container` (not yet deployed):
  `""` (caller falls back to stored value)
- **compose app, app_service set, service in exposed_ports**:
  `nanoku-<app>-<service>-1:<port>` — compose's default replica-1
  suffix; multi-replica (v2) will need a different pick.
- **compose app, no app_service, exactly one exposed port**: same
  shape, auto-picked.
- **compose app, no app_service, multiple exposed ports**: `""` —
  the editor must require a service pick.
- **compose app, app_service not in exposed_ports**: `""` — falls
  through to the stored upstream so a stale service name doesn't
  blank the Caddyfile.

Two consumers:

1. `resolveSiteUpstreamsCtx` (Caddyfile render) — recomputes on
   every render, never writes back. The Caddyfile is always
   consistent with the joined app state.
2. `RefreshSitesForApp` — recomputes and writes back to
   `Site.Upstream` for every site linked to a given app. Called
   from `executeDeploy` (after the Caddyfile regen) and from
   `UpdateApp` when `exposed_ports` or `deploy_method` changes.
   The stored `Site.Upstream` is what the UI list view shows;
   keeping it fresh avoids a stale list while the Caddyfile is
   correct.

## Deploy-time validation

`executeDeploy` (in `internal/api/deploy_executor.go`) validates
that every service in `App.exposed_ports` is actually running after
`docker compose up` finishes. The check uses
`composeServiceContainerName(app, service) == "nanoku-<app>-<service>-1"`,
which is the convention compose itself uses to name the replica-1
container. A missing service fails the deploy with a clear message
("exposed_ports reference services not in stack: web, db") rather
than silently letting Caddy route to a non-existent container.

## UX (sites.tsx)

The site editor is now driven by app selection:

- **No app picked** → free-upstream mode. Upstream field is
  editable, required.
- **Docker app picked** → upstream is locked, auto-resolves to
  `<current_container>:<port>`. The "extra" hint shows
  "Upstream auto-resolved from the app's current container."
- **Compose app picked, 1 exposed_port** → upstream is locked,
  auto-picks the only service. The "extra" hint shows the full
  upstream string (`nanoku-<app>-<service>-1:<port>`) so the
  operator can sanity-check the value.
- **Compose app picked, N>1 exposed_ports** → a new **Service**
  select appears between App and Upstream, required. The user must
  pick which service the site proxies to. The Service field uses
  the same `shouldUpdate` listener as the App field, so adding
  exposed_ports to an existing compose app mid-edit re-evaluates
  the form.

The App select labels carry the load-bearing info:

- `blog · docker · nginx:1.27 :80` (docker)
- `kuma · compose · 1 service: uptime-kuma:3001` (auto-pick)
- `stack · compose · 3 services` (needs a pick)
- `stack · compose · (no exposed ports)` (free upstream only)

The ExposedPortsSection has an "Import from compose" button that
scaffolds the row list by parsing the stored compose YAML.
`internal/api/compose_import.go:ImportExposedPortsFromCompose` walks
`services:` and picks the most likely container-side port per
service, in this priority:

1. `expose[0]` — the operator's explicit "this is what Caddy
   should hit" hint.
2. `ports[0]` container half — for `"8080:80"` the answer is
   `80` (not 8080, which is the host port and irrelevant on the
   nanoku internal network).
3. `0` — surfaces an empty input in the editor so the operator
   fills it in. Skipping the service entirely would hide
   misconfigurations; the UI shows a "Y of N need a port" hint
   on import.

The button is only available when editing an existing app
(server endpoint requires an app id). For a new app the operator
saves the compose content first and re-opens the editor to
import. The endpoint also refuses compose apps that set
`compose_path` (the parser only reads inline content); the
response names the path so the operator knows what to paste.

The list view's upstream column now shows two lines: the actual
upstream string in muted mono on top, and a small annotation
beneath:

- docker: `from app · blog`
- compose: `from stack · service web`

The editor also accepts `appService` for docker apps but silently
drops it — the field is a no-op so a future docker→compose switch
on the same site doesn't have to remember to clear it.

## Tests

- `internal/api/exposed_ports_test.go` — input validator + marshaller
  (sort, dedup, range, empty).
- `internal/api/sites_test.go` — end-to-end via the actual HTTP
  handlers, covering:
  - docker site resolves from current_container
  - compose site auto-picks the single service
  - compose site uses the explicit service pick
  - invalid service is 400 (not silent fallback)
  - docker-mode ignores appService
  - PATCH flips the service and rewrites the upstream
  - `clearApp: true` detaches and clears
  - PATCH that doesn't touch the app edge does NOT silently
    rewrite the upstream (contract guard)
  - `RefreshSitesForApp` rewrites a stale upstream after a
    container roll
- `ui/src/routes/sites.helpers.test.ts` — `appOptionLabel` /
  `serviceOptionLabel` shape (docker / compose-single /
  compose-multi / compose-no-ports).

## Migration

No migration script. The columns are nullable; existing rows have
NULL on both new fields, which is the same as "no exposed ports" /
"no service set". An existing docker app with a linked site keeps
working unchanged (current_container path). An existing compose
app that was already linked to a site now resolves via the new
formula on the next render — and for those legacy compose sites
with `current_container` set, the auto-pick / fall-through rules
keep the Caddyfile valid as long as the app has at most one
exposed_port, which is the common case.

## What we explicitly did NOT do

- **No `AppEndpoint` table.** The JSON column is enough; a join
  table is overkill until we need per-service settings.
- **No path-based routing.** The Site model is one domain = one
  upstream; multi-service fan-out via `route /api/*` is a v2
  concern.
- **No multi-replica support.** The compose service container
  name hardcodes the `-1` suffix; v2 can extend the resolution
  to honor `deploy.replicas`.
- **No API for the "warning" amber message** in the form when an
  app has 0 exposed_ports. The UI shows a regular placeholder
  instead, since the case is rare and the placeholder text is
  self-explanatory.
