# Nanoku 架构分析与优化建议

我已通读了前后端核心代码。这个项目整体设计干净、聚焦（"nano Coolify" 定位明确），ent + Go 1.22 pattern mux + TanStack Router 的技术选型很合理。下面分**后端**和**前端**列出上线前值得优化的点，按优先级排序。

## 🔴 后端（Go）

### 1. Docker Manager 通过 shelling out `docker` CLI 调用 — 这是最大的架构性风险

`internal/docker/docker.go` 所有操作都是 `exec.CommandContext("docker", ...)`。问题：
- **依赖宿主机 PATH 里有 `docker`**，且没有版本协商；用户 docker 版本不一致时 `--format` 模板可能失效。
- 每个 `stats`/`ps` 调用都 fork 一个进程，**dashboard 的 `AllStats` 对每个容器名又拼到 argv**（`docker stats ... name1 name2 ...`），容器多了会撞 ARG_MAX。
- **stdout 解析脆弱**：`docker run` 的输出靠 `strings.TrimSpace(out)` 取 ID，但 `run()` 把 stdout+stderr 拼在一起（`stdout.String() + stderr.String()`），一旦镜像有 warning 进到 stderr，ID 解析就会出错。

**建议**：改用官方 Go SDK [`github.com/docker/docker/client`](https://pkg.go.dev/github.com/docker/docker/client)。这是 Coolify/Dokploy 都在用的方案，能拿到 typed API、流式日志、events。迁移成本中等但收益巨大：消除进程解析脆弱性、支持 `docker stats` 流式订阅、支持 healthcheck/事件监听。如果暂时不想全量迁移，**至少先修 `run()` 不要把 stderr 拼进 stdout**（现在是 `out := stdout.String() + stderr.String()`，这对取 container ID 是 bug 源）。

### 2. HTTP 层没有结构化错误 / 没有请求日志 / 没有中间件链

`main.go:86-141` 的路由是裸 `mux.HandleFunc` 列表，几十条路由挤在 main 里。问题：
- **没有请求日志、没有 request-id、没有 panic recovery**。一个 handler panic 会直接断连，前端只看到网络错误。
- **没有超时中间件**：`DeployApp`、`PullImage` 是同步阻塞在 HTTP handler 里的（`apps.go:609` 的 `DeployApp` 不像 trigger 那样异步），一个慢 pull 会把 HTTP 连接挂死到 docker pull 完成。
- `writeErr` 直接把 `err.Error()` 透传给客户端，**会泄漏内部错误**（例如 ent 的 SQL 错误、文件路径）。

**建议**：
- 抽一个 `router.go`，把路由注册从 main 移到 `api.NewRouter(h, sessions)`，main 只负责 wire。
- 加 `loggingMiddleware`（request-id + method + path + status + duration）和 `recoverMiddleware`。
- `writeErr` 引入 error code 体系，对外只暴露稳定 message，内部错误打日志。

### 3. 同步 deploy 会阻塞 HTTP 连接，与异步 trigger 行为不一致

`DeployApp`（`apps.go:609`）是**同步**的：`PullImage` + `CreateAppContainer` 全在请求生命周期内完成，而 `Trigger`（`trigger.go:128`）是 `go h.executeTriggerDeploy(...)` 异步。

两条路径做的是几乎一样的事，但：
- UI 点 "Deploy" 会一直转圈到 pull 完成（可能几分钟），期间断网就丢了状态。
- trigger 的 deploy 记录有完整 `running → success/failed` 生命周期，**手动 deploy 的记录在失败时也是 `markDeployFailed`，但成功路径里 caddy regen 失败只 `markDeployFailed` 不返回错误**（`apps.go:772-774`），状态会变成 failed 但 HTTP 已经返回 201 —— 前端会困惑。
- `DeployLock` 只在 trigger 路径用，**手动 deploy 完全不持锁**（`apps.go:609` 没有 `TryAcquire`）。同时点 deploy + 触发 trigger 会竞态改 `current_container`。

**建议**：统一成一条 deploy 管线。手动 deploy 也走异步：创建 `running` 记录 → 返回 202 → goroutine 复用 `executeTriggerDeploy` 的核心逻辑。手动 deploy 也 `TryAcquire`，拿不到就返回 409。这样前端可以轮询 `GET /api/apps/{id}/deployments` 看进度，不用靠 spinner。

### 4. N+1 查询遍地都是，dashboard 尤其严重

`dashboard.go:106-133`：每个 app 都 `a.QueryCurrentContainer().Only(ctx)`（1 次查询）+ `h.Docker.ContainerStatus(ctx, cur.Name)`（1 次 docker exec）。20 个 app = 20 次 DB 查询 + 20 次 `docker ps` fork。

`apps.go:311-322` `ListApps` 同样：每个 app 在 `toAppDTO` 里 `a.QueryCurrentContainer().Only(ctx)` + `Docker.ContainerStatus`。

`apps.go:436-443` `GetApp` 又单独查 env vars（`toAppDTO` 已经查了 volumes，但 env 是在 handler 里再查一次）。

**建议**：
- `ListApps` 用 `h.DB.App.Query().WithCurrentContainer().WithVolumes().Order(app.ByName()).All(ctx)` 一次 eager load。
- `ContainerStatus` 批量化：`AllStats` 已经会跑一次 `docker ps --filter name=nanoku-`，可以复用这个结果做状态映射，而不是每个 container 再 fork 一次。或者干脆信任 DB 里的 `container.status`（start/stop 时已经更新了），只在显式 refresh 时去查 docker。

### 5. `pathID` 手动字符串切割 — 该用 mux 的 PathValue

`handlers.go:351-359` 的 `pathID` 是 `strings.TrimPrefix(path, "/api/sites/")` + `SplitN`，在 5+ 个 handler 里重复。Go 1.22 的 pattern mux 已经支持 `{id}` 占位，trigger.go 里 `r.PathValue("name")` 已经在用了。

**建议**：路由改成 `mux.HandleFunc("GET /api/apps/{id}", ...)`，handler 里 `r.PathValue("id")` + 一次 `strconv.Atoi`。删掉 `pathID` 函数和它所有的 `strings.TrimPrefix` 调用。一致性 + 健壮性都更好（现在 `/api/apps/abc/env` 会被 `pathID` 当 id=0 处理）。

### 6. 配置加载：`AdminPassword` 不再 required，与 AGENTS.md 矛盾

`config.go:39` 现在 `AdminPassword: os.Getenv("NANOKU_ADMIN_PASSWORD")`（没有 fallback），而 `AGENTS.md` 写着 "NANOKU_ADMIN_PASSWORD is required at startup"。`SeedFirstAdmin`（`users.go:65`）在 password 为空时**静默 return nil** —— 也就是说现在空密码能启动，只是不 seed admin。如果 DB 已有用户则完全无影响，但 fresh install 时空密码启动 = 无法登录。

另外 `flag.Parse()` 和 env 混合：`--listen` 能覆盖 env，但 `--db`、`--caddyfile` 也能覆盖，而 `NANOKU_ADMIN_PASSWORD` 没有 flag 对应。配置来源不统一。

**建议**：决定一个来源（推荐 env 优先，flag 作为 override 但只在显式设置时覆盖）。如果保留 seed 机制，启动时检测 "DB 无用户且 env 无密码" 应该 fail fast，而不是静默启动。

### 7. 敏感数据存储

- `registry_password` schema 里有 `// TODO: encrypt at rest before any real deployment`（`schema/app.go:58`）。明文存数据库。
- `compose_path` 接受任意绝对路径（`apps.go:371`），用户可以指向 `/etc/shadow` 然后 deploy 时 nanoku 会 `docker compose -f /etc/shadow up` —— 虽然会失败，但路径注入面存在。建议限制在 `ComposeBaseDir` 下或做 allowlist。

### 8. CORS `Allow-Origin: *` + session cookie

`auth.go:50-61` 的 CORS 是 `*`，但认证改成了 cookie（`SameSite=Strict`）。`SameSite=Strict` 会挡掉跨站请求，所以实际上 cookie 不会被第三方站点带上 —— 这救了你。但 `Allow-Origin: *` 配合 `credentials: 'include'` 在浏览器规范里是**无效组合**（浏览器会拒绝带 cookie 的响应）。现在能工作是因为前端和后端同源（embed 在一起）。

**建议**：既然是同源部署，直接**删掉 CORS 中间件**或收紧到同源。保留 `*` 是给 dev 用的，那应该用 env flag 在 dev 开、prod 关。

### 9. 缺少后台任务/清理机制

- Session 过期清理只在启动时跑一次（`main.go:70`），运行期间不再清理。
- 没有 deploy 记录清理 —— `ListAppDeploys` 只 `Limit(50)`，但表会无限增长。
- 没有 orphan container 清理：如果 nanoku 崩溃在 deploy 中途，DB 里的 container 行可能和 docker 实际状态不一致，没有任何 reconcile 机制。

**建议**：加一个轻量 background ticker（启动一个 goroutine，每 6 小时清过期 session + 清 N 天前 deploy 记录）。container reconcile 可以做成 `/api/system/reconcile` 手动触发。

### 10. 测试结构

测试只有 1679 行覆盖 docker CLI 解析 + 几个 handler。**核心业务路径缺测试**：
- `DeployApp` / `containerAction` 完全没测（最难测因为依赖 docker，但可以用接口抽离后 mock）。
- `caddy.Render` 有测但 `regenerateAndReload` 链路没测。
- 前端 **0 个测试**（`ui/src` 下没有 `.test.tsx`）。

**建议**：把 `docker.Manager` 抽成接口（`type Deployer interface { PullImage(...); CreateAppContainer(...); ... }`），handler 持有接口，测试注入 fake。这样 `DeployApp` 的状态机就能单测了。前端至少给 `api.ts` 的 error 处理和 `auth.ts` 加测。

---

## 🔴 前端（React/TS）

### 1. 没有任何数据层抽象 — 每个页面手写 `useState` + `useEffect` + fetch

`sites.tsx`、`apps.tsx`、`dashboard.tsx`、`system.tsx` 每个都重复同样的模式：
```ts
const [data, setData] = useState(...)
const [loading, setLoading] = useState(true)
const reload = useCallback(async () => { setLoading(true); try {...} finally {setLoading(false)} }, [])
useEffect(() => { void reload() }, [reload])
```

问题：
- **没有缓存**：apps 页面切到 sites 再切回 apps，重新 fetch 全量。
- **没有乐观更新**：每个 action 都 `await fn(); reload()`，体验慢。
- **401 处理散落**：每个 reload 里都写 `if (!(err instanceof ApiError && err.status === 401))`。
- **没有后台失效**：dashboard 数据不会自动刷新，用户必须手动点 refresh。

**建议**：引入 TanStack Query（项目已经在用 TanStack Router，生态一致）。`useQuery(['apps'], api.listApps)` + `useMutation`。能拿到：缓存、自动 refetch、invalidation、乐观更新、loading/error 状态、`queryClient.invalidateQueries` 在 mutation 后自动刷新关联查询。这一步能删掉每个页面 30% 的样板代码。

### 2. `apps.tsx` 单文件 1484 行 — 严重需要拆分

一个文件里塞了：`AppsPage`、`DeployMethodSwitch`、`RegistrySection`、`TriggerSection`、`CopyableValue`、`TriggerTokenModal`、`StatusCell`、`EmptyState`、`AppDetail`（含 env/volumes/deploys/logs 4 个 tab）、`Field`。

`AppDetail` 本身就是一个 500 行的组件，包含 env editor、volume editor、deploy list、logs viewer 四套独立逻辑。

**建议**目录结构：
```
ui/src/routes/apps/
  index.tsx          // AppsPage (列表)
  [id].tsx           // AppDetail 路由
ui/src/features/apps/
  AppForm.tsx        // create/edit 表单
  RegistrySection.tsx
  TriggerSection.tsx
  AppDetail/
    OverviewTab.tsx
    EnvTab.tsx
    VolumesTab.tsx
    DeploysTab.tsx
    LogsTab.tsx
```
TanStack Router 的 file-based routing 已经支持这个结构。顺便把 AppDetail 从 Drawer 升级成独立路由（`/apps/$id`），这样能分享 app 详情链接、浏览器后退能回列表。

### 3. 前端 auth 状态管理脆弱

`auth.ts` 用一个模块级 `let authed = false` 布尔。问题：
- `ensureAuth()` 在 `main.tsx:30` 启动时 fire-and-forget（`void ensureAuth()`），**没有 await**。如果用户直接访问 `/apps`，路由 guard 的 `beforeLoad` 里 `isAuthenticated()` 可能还是 false（因为 ensureAuth 没完成），就会 redirect 到 login，**然后 ensureAuth 完成后 authed=true 但人已经被踢走了**。
- `markLoggedOut` 在 `api.ts:38` 每次收到 401 就调用，但没有协调：如果同时 5 个请求都 401，`onUnauthorized?.()` 会被调用 5 次，触发 5 次 `router.navigate('/login')`。

**建议**：auth 状态放进 TanStack Query / context，`ensureAuth` 改成可 await 的 promise（在 router 的 `beforeLoad` 里 `await ensureAuth()`）。或者更简单：路由 guard 直接 `await api.me()`，不依赖内存 flag。`onUnauthorized` 加去重。

### 4. 类型与后端 DTO 手工同步 — 容易漂移

`types.ts` 是手写的，和后端 `AppDTO`/`SiteDTO` 字段一一对应但靠人维护。已经能看到漂移迹象：`DashboardAppDTO` 有 `stats` 字段，`types.ts` 的 `DashboardApp` 也有，但 `ContainerStats` 在 types 里和 `ContainerStatsDTO` 字段一致 —— 任何一边改了另一边不会报错。

**建议**：用 `openapi-typescript` 或 zod 从后端 schema 生成。如果不想引入 OpenAPI，至少加一个 build-time 脚本用 `ts-to-zod` 生成 schema 并在 api 层 parse 响应，运行时就能发现漂移。最低成本方案：在 CI 加一个测试，fetch 后端 `/api` 返回的 shape 和前端 types 比对。

### 5. 缺少 Suspense / loading 边界

每个页面自己管 loading，AppDetail 打开时先显示 "loading"，数据到了再渲染。TanStack Router + React 19 支持 `pendingComponent` / Suspense，可以让数据加载和路由切换解耦。配合 TanStack Query 的 `useSuspenseQuery`，组件里就不需要 `if (loading) return <Spinner/>` 了。

### 6. i18n 加载方式

`i18n/index.ts` 没读但 `TopNav` 里有语言切换。需要确认是不是同步加载所有 locale。如果用 `import` 动态加载按需，首屏会快。Antd 的 `zhCN`/`enUS` locale 也是全量 import 的（`__root.tsx:3-4`），可以改 lazy。

### 7. 前端缺测试

0 个 UI 测试。至少给：
- `api.ts` 的 `request` 函数（401 处理、错误解析、Content-Type 分流）
- `auth.ts` 的状态机
- `apps.tsx` 的 `volumeRowInvalid` / `buildTriggerCurl` / `buildWorkflowYaml`（这些是纯函数，最容易测）

---

## 🟡 横切关注点

| 项 | 现状 | 建议 |
|---|---|---|
| **可观测性** | 只有 `log.Printf` | 加结构化日志（slog），request-id 贯穿，deploy 记录里存 request-id |
| **优雅关闭** | `srv.Shutdown` 有，但**没等后台 deploy goroutine** | 维护 in-flight deploy 的 WaitGroup，shutdown 时等完或标记取消 |
| **DB 迁移** | `Schema.Create` 建表，没有版本化 | 用 ent 的 atlas migration（已依赖了 `ariga.io/atlas`），生成 versioned migration 文件 |
| **健康检查** | 没有 `/healthz` | 加一个不鉴权的 liveness 端点（只查 DB ping） |
| **配置校验** | 只有 CaddyMode 校验 | 启动时校验 ComposeBaseDir 可写、DBPath 父目录存在 |
| **Volume schema 没加 index** | `schema/volume.go` 缺 `app` 外键索引 | 查 app volumes 会全表扫，加 `index.Fields("app_volumes")` |

---

## 优先级建议（上线前必做 → 可延后）

**P0（上线前）**
1. 修 `docker.run()` 的 stdout/stderr 拼接 bug（一行改动，但影响所有 docker 调用）
2. `DeployApp` 也加 `DeployLock`，避免手动+trigger 竞态
3. `writeErr` 不透传内部错误（泄漏路径/SQL）
4. 空密码启动应 fail fast（fresh install 会被锁死）
5. `pathID` → `PathValue`，避免 `/api/apps/abc` 被当 id=0

**P1（上线后第一迭代）**
6. Docker CLI → SDK 迁移
7. 引入 TanStack Query，统一数据层
8. 拆分 `apps.tsx`
9. 统一 deploy 管线（手动 deploy 也异步）
10. 加请求日志 + panic recovery 中间件

**P2（架构演进）**
11. `docker.Manager` 接口化 + 业务逻辑单测
12. OpenAPI schema → 前端类型生成
13. versioned migration
14. 后台清理任务（session/deploy 记录/orphan container）

---

需要我针对其中某一项给出具体实现方案或直接动手改吗？比如 P0 那 5 条我可以一次性出 PR。