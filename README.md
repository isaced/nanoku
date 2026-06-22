# Nanoku

**The ultra-lightweight self-hosted deployment hub for modern frontends.**

Nanoku is a minimal yet powerful PaaS-like tool that brings a smooth Vercel/Dokku-style experience to your own server — without the bloat.

### ✨ Features

- **Single binary deployment** — Built with Go, everything (including the web UI) in one executable
- **Embedded Admin UI** — Clean, lightweight web interface for managing projects
- **SQLite powered** — Zero external database required
- **Provider-agnostic HTTP trigger** — `git push` → CI builds image → POST to nanoku → deploy
- **Visual configuration** — Easily manage domains, build settings, and reverse proxy rules
- **Frontend focused** — Perfect for Vite, Next.js, React, Vue, Svelte, and other static/SPA projects
- **Caddy integration ready** — Automatic config generation and reload support
- **Minimal resource usage** — Designed to run efficiently even on small VPS

### Philosophy

While Coolify and Dokploy are great, sometimes you just want something **truly lightweight**.  
Nanoku is the nano version: less features, less overhead, but retains the core joy of “push → deployed”.

### Quick Start

```bash
# Download the latest binary
curl -L -o nanoku https://github.com/yourname/nanoku/releases/latest/download/nanoku
chmod +x nanoku

# Start it
./nanoku
```

### Auto-deploy from CI

Nanoku doesn't build your code — it pulls pre-built images. The build runs in
**your** CI (GitHub Actions, GitLab CI, Drone, anything that can POST JSON);
nanoku just exposes a small HTTP endpoint that triggers a deploy when called.

#### Protocol

```http
POST /api/apps/{name}/trigger
Authorization: Bearer <token>
Content-Type: application/json

{"tag": "v1.2.3", "commit_message": "fix: ..."}
```

- `{name}` is the app's DNS-1123 name (set on create)
- `<token>` is the per-app bearer token, shown once when you enable the trigger
- The `tag` becomes the image tag; nanoku pulls `<app.image repo part>:<tag>`
- Response is `202 Accepted` with the new deploy id, or `409` if a deploy for
  that app is already running (a single deploy at a time is enforced)
- The token is compared with `crypto/subtle.ConstantTimeCompare` — only exact
  matches work, no prefix / suffix tricks

#### Setup

1. In nanoku, create an app and tick **Enable HTTP trigger**. Save the token
   shown in the popup — you won't see it again.
2. In your CI, set two env vars:
   - `NANOKU_TRIGGER_URL`: `https://your-nanoku/api/apps/<name>/trigger`
   - `NANOKU_TRIGGER_TOKEN`: the token from step 1
3. Build & push your image, then POST the URL with the token.

#### GitHub Actions example

Drop this into your app repo at `.github/workflows/deploy.yml`:

```yaml
name: deploy
on:
  push:
    branches: [main]

jobs:
  build:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v4

      - uses: docker/setup-buildx-action@v3

      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - uses: docker/build-push-action@v5
        with:
          push: true
          tags: ghcr.io/${{ github.repository_owner }}/${{ github.event.repository.name }}:${{ github.sha }}

      - name: Notify Nanoku
        env:
          URL: ${{ vars.NANOKU_TRIGGER_URL }}
          TOKEN: ${{ vars.NANOKU_TRIGGER_TOKEN }}
          SHA: ${{ github.sha }}
          MSG: ${{ github.event.head_commit.message }}
        run: |
          BODY=$(jq -nc --arg t "$SHA" --arg m "$MSG" '{tag: $t, commit_message: $m}')
          curl -fsS -X POST "$URL" \
            -H "Authorization: Bearer $TOKEN" \
            -H "Content-Type: application/json" \
            -d "$BODY"
```

#### Generic shell (any CI / local)

```bash
BODY='{"tag":"v1.2.3","commit_message":"fix: ..."}'
curl -fsS -X POST "$NANOKU_TRIGGER_URL" \
  -H "Authorization: Bearer $NANOKU_TRIGGER_TOKEN" \
  -H "Content-Type: application/json" \
  -d "$BODY"
```

#### Private registries

Add matching registry credentials in the nanoku app form (Registry URL +
username + password). The trigger worker logs into the registry before pull
and logs out after, so credentials don't linger in `~/.docker/config.json`.
