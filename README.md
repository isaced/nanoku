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
