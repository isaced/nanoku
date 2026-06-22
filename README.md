# Nanoku

**The ultra-lightweight self-hosted deployment hub for modern frontends.**

Nanoku is a minimal yet powerful PaaS-like tool that brings a smooth Vercel/Dokku-style experience to your own server — without the bloat.

### ✨ Features

- **Single binary deployment** — Built with Go, everything (including the web UI) in one executable
- **Embedded Admin UI** — Clean, lightweight web interface for managing projects
- **SQLite powered** — Zero external database required
- **GitHub-native workflow** — Webhook driven: `git push` → automatic build → deploy
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

### Auto-deploy from GitHub

Nanoku doesn't build your code — it pulls pre-built images. The build runs in
**your** GitHub Actions; nanoku just receives a webhook with the image tag.

**Setup:**

1. In nanoku, create an app with `Image repository` set, e.g. `ghcr.io/you/myapp`.
   Save the secret shown in the popup (you won't see it again).
2. In your app repo on GitHub: **Settings → Secrets and variables → Actions**:
   - Variable `NANOKU_WEBHOOK_URL`: `https://your-nanoku/api/webhook/myapp`
   - Secret `NANOKU_WEBHOOK_SECRET`: the secret from step 1
3. Drop this workflow into your app repo at `.github/workflows/deploy.yml`:

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
          URL: ${{ vars.NANOKU_WEBHOOK_URL }}
          SECRET: ${{ vars.NANOKU_WEBHOOK_SECRET }}
          SHA: ${{ github.sha }}
          MSG: ${{ github.event.head_commit.message }}
        run: |
          BODY="{\"tag\":\"${SHA}\",\"commit_message\":\"${MSG//\"/\\\"}\"}"
          SIG="sha256=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" | awk '{print $2}')"
          curl -fsS -X POST "$URL" \
            -H "Content-Type: application/json" \
            -H "X-Hub-Signature-256: $SIG" \
            -d "$BODY"
```

On push: GitHub builds the image, pushes to ghcr.io, then notifies nanoku.
Nanoku pulls the new image and redeploys.

Private registries: set `imageRepo` to your private registry path and add
matching registry credentials in the nanoku app form.
