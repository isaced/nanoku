# Nanoku v0 — MVP 计划

## 目标

最小可用 Nanoku：Docker 化的 Go 单二进制 + 嵌入式管理 UI（AntDesign + Tailwind）+ SQLite，让用户通过浏览器管理反向代理条目（domain → upstream），Caddyfile 由程序自动生成并触发 Caddy reload。

不实现 README 里吹的 GitHub webhook / 自动 build / 自动 deploy——那是后续版本。

## Scope

### In (v0)

- 单 Go 二进制，admin HTTP 服务（默认 `:8080`）
- Docker SDK 集成：自动管理 Caddy 容器（默认 managed 模式）
- 外部 Caddy 兼容模式（写 Caddyfile + `--watch` 自动 reload）
- 嵌入式管理 UI（React + AntDesign + Tailwind + Vite）
- SQLite 存储站点（domain / upstream）
- 自动生成 Caddyfile
- Basic auth（env var 单密码）
- 启动文档（README + Makefile）

### Out (v0 不做)

- GitHub webhook / 自动 build / 部署
- App 容器管理（v1 引入）
- 多用户、RBAC、SSO
- SSL UI 配置（Caddy 自动处理）
- 日志 / metrics / 监控
- Docker / k8s 自身打包（v0 是裸 Go 二进制）
- ACME email UI 编辑

## 架构

两个角色：nanoku 二进制 + Caddy 进程。Caddy 跑不跑在 Docker 里看模式。

```
┌─────────────────────────────────────────────────────────┐
│ Host (Docker daemon)                                    │
│                                                         │
│  ┌──────────────────────────────────────────────────┐  │
│  │ nanoku (binary or container)                     │  │
│  │ - admin HTTP :8080                               │  │
│  │ - mounts /var/run/docker.sock                    │  │
│  │ - SQLite at ./nanoku.db (or NANOKU_DB)           │  │
│  │ - writes Caddyfile to disk                       │  │
│  └──────────────────────────────────────────────────┘  │
│       │                                                 │
│       │ managed mode: starts nanoku-caddy container     │
│       │ external mode: only writes Caddyfile            │
│       ▼                                                 │
│  ┌──────────────────────────────────────────────────┐  │
│  │ caddy (managed container OR external process)    │  │
│  │ - reads Caddyfile (with --watch)                 │  │
│  │ - serves :80 / :443                              │  │
│  │ - labels: nanoku.managed=true, nanoku.role=caddy │  │
│  └──────────────────────────────────────────────────┘  │
│                                                         │
│  Future (v1): nanoku-net Docker network for app 容器    │
└─────────────────────────────────────────────────────────┘
```

## 目录结构

```
nanoku/
├── main.go                # 入口 + 路由组装
├── go.mod
├── go.sum
├── Makefile               # build-ui / build / run / dev
├── README.md
├── ui/                    # 前端工程（用户自己 init）
│   ├── package.json
│   ├── vite.config.ts
│   ├── tailwind.config.js
│   ├── src/
│   └── dist/              # build 产物，go:embed 入口
├── internal/
│   ├── config/config.go   # flag / env 解析
│   ├── db/
│   │   ├── db.go          # SQLite open + schema + CRUD
│   │   └── migrations.go  # 手撸迁移框架（v0.1+ 启用）
│   ├── caddy/
│   │   ├── caddyfile.go   # 从 sites 生成 Caddyfile 字符串
│   │   └── writer.go      # 原子写文件（write tmp + rename）
│   ├── docker/
│   │   └── client.go      # moby client 封装 + ensure caddy 容器
│   ├── api/
│   │   ├── handlers.go    # /api/sites, /api/status
│   │   ├── auth.go        # basic auth middleware
│   │   └── ui.go          # 嵌入式 UI 静态文件服务 + SPA fallback
│   └── server/server.go   # http.ServeMux 组装
└── docs/
    └── PLAN-v0.md
```

## 关键决策

| 项 | 选择 | 理由 |
|---|---|---|
| Caddy 模式 | `managed`（默认）+ `external` 可切换 | 兼顾 greenfield 与已有 Caddy 用户 |
| Caddy reload | Caddy `--watch` 自动 reload | nanoku 只写文件，不 exec 不 reload |
| Docker SDK | `github.com/docker/docker/client` (moby) | 官方维护，标准选择 |
| UI 技术栈 | React + AntDesign + Tailwind + Vite | 用户指定；Vite 构建快，AntDesign 组件齐 |
| SQLite 驱动 | `modernc.org/sqlite` | 纯 Go 无 CGO，保单二进制 |
| 迁移框架 | v0 无；v0.1+ 手撸 ~50 行 | 当前一张表，`CREATE TABLE IF NOT EXISTS` 够用 |
| 认证 | HTTP Basic Auth，env var 密码 | 最简单，前端 fetch 直接带 header |
| Admin 端口 | `:8080` | 避开 80/443 给 Caddy |
| 失败处理 | DB 事务失败回滚；Caddyfile 写失败返 500 | 数据一致性优先；Caddy --watch 自愈 |

## 数据模型 (v0)

```sql
CREATE TABLE IF NOT EXISTS sites (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    domain     TEXT NOT NULL UNIQUE,
    upstream   TEXT NOT NULL,
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_sites_domain ON sites(domain);
```

未来 v1 扩展：`containers`、`deploys`、`app_configs` 等。届时启用迁移框架。

## API Contract

```typescript
type Site = {
  id: number;
  domain: string;
  upstream: string;
  enabled: boolean;
  createdAt: string;  // ISO 8601
  updatedAt: string;
};

type Status = {
  caddyMode: 'managed' | 'external';
  dockerConnected: boolean;
  caddyHealthy: boolean;
  caddyContainerId?: string;
  siteCount: number;
};
```

| Method | Path | Body / Response |
|---|---|---|
| GET | `/api/sites` | → `Site[]` |
| POST | `/api/sites` | body `{domain, upstream}` → `Site` |
| PUT | `/api/sites/:id` | body `{domain?, upstream?, enabled?}` → `Site` |
| DELETE | `/api/sites/:id` | → 204 |
| GET | `/api/status` | → `Status` |

所有 `/api/*` + `/` 走 Basic Auth middleware。

## 配置 (env vars / flags)

| Var / Flag | Default | 说明 |
|---|---|---|
| `NANOKU_LISTEN` | `:8080` | admin HTTP listen |
| `NANOKU_DB` | `./nanoku.db` | SQLite 文件路径 |
| `NANOKU_CADDYFILE` | `./Caddyfile` | 生成的 Caddyfile 路径 |
| `NANOKU_CADDY_MODE` | `managed` | `managed` \| `external` |
| `NANOKU_CADDY_IMAGE` | `caddy:2` | managed 模式用的镜像 |
| `NANOKU_CADDY_CONTAINER` | `nanoku-caddy` | managed 模式的容器名 |
| `NANOKU_CADDY_NETWORK` | `nanoku-net` | managed 模式的 Docker network |
| `NANOKU_ACME_EMAIL` | (空) | Caddy ACME 注册邮箱 |
| `NANOKU_ADMIN_USER` | `admin` | Basic Auth 用户名 |
| `NANOKU_ADMIN_PASSWORD` | (必填) | Basic Auth 密码 |
| `DOCKER_HOST` | `/var/run/docker.sock` | Docker daemon 地址（标准 env） |

## 生成 Caddyfile 示例

```caddyfile
# Auto-generated by Nanoku. Do not edit.
{
    email admin@example.com
}

foo.example.com {
    reverse_proxy localhost:3000
}

bar.example.com {
    reverse_proxy 192.168.1.10:8080
}
```

Caddy 启动命令：

```bash
# managed 模式：nanoku 自动起容器，命令大致是
docker run -d --name nanoku-caddy \
  -v nanoku-caddy-data:/data \
  -v nanoku-caddy-config:/config \
  -v <Caddyfile 路径>:/etc/caddy/Caddyfile \
  -p 80:80 -p 443:443 \
  --label nanoku.managed=true \
  --label nanoku.role=caddy \
  caddy:2 caddy run --watch --config /etc/caddy/Caddyfile

# external 模式：用户自己跑，例
caddy run --watch --config /etc/caddy/Caddyfile
```

## 验收

1. `./nanoku` 启动 → 监听 :8080
2. 浏览器打开 → Basic Auth 弹窗 → 输入密码进入 UI
3. 创 site：`domain=foo.test` / `upstream=localhost:3000`
4. 检查 Caddyfile 含 `foo.test { reverse_proxy localhost:3000 }`
5. managed 模式：`docker ps` 看到 `nanoku-caddy` 容器在跑
6. external 模式：用户的 Caddy `--watch` 拾到变化 reload
7. 删 site → Caddyfile 同步移除 → Caddy reload
8. 重启 nanoku → 数据仍在（SQLite 持久化）
9. `GET /api/status` 返回正确 docker 连接状态 / caddy 健康状态

## 工作量估算

~600-700 行 Go（多了 Docker + 容器管理 + UI 嵌入）+ 用户自己搞前端。1-1.5 天。

## 实现顺序

1. `internal/config` — 解析所有 flag/env
2. `internal/db` — SQLite open + sites 表 CRUD
3. `internal/caddy` — Caddyfile 生成器 + 原子写
4. `internal/docker` — moby client + ensureCaddyContainer()
5. `internal/api/auth` — Basic Auth middleware
6. `internal/api/handlers` — sites + status endpoints
7. `internal/api/ui` — embed FS + SPA fallback
8. `internal/server` — ServeMux 组装
9. `main.go` — wire 起来
10. Makefile — `build-ui` / `build` / `run` / `dev`
11. README — quickstart + 两种 Caddy 模式的说明
12. 端到端 smoke：managed + external 各跑一遍

## 下一步

确认这两点后开工：

1. **Caddy 模式默认值**：默认 `managed`（greenfield 友好，已有 Caddy 用户切 `external`）可以接受吗？
2. **Docker SDK 路径**：用 `github.com/docker/docker/client`（moby）OK 吗？

确认后开干。