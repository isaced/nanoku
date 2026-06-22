# Nanoku — GitHub Webhook + 外部 Build 方案 v2

> v2 模型：nanoku **不**做 build。用户在 app repo 自己的 GitHub Actions 里 build + push image，然后通知 nanoku pull + run。

## 1. 为什么改模型

v1 方案（`docs/PROPOSAL-webhook.md`）让 nanoku 自己 clone + build，违反「ultra-lightweight」原则：
- nanoku 容器要装 git（+25 MB）
- 需要 build context 磁盘 + cache 管理
- build 拖慢小机器的 CPU
- build 失败期间 nanoku 机器被占

v2：build 责任外移到用户 app repo 的 GitHub Actions（在 GitHub 免费 runner 上跑），nanoku 只做 image pull + container run。这才是 Coolify / Dokploy / Dokku 的「**接收预构建 artifact**」路线。

## 2. 数据流

```
Developer: git push origin main
   ↓
GitHub 触发 app repo 的 .github/workflows/deploy.yml:
   1. checkout
   2. docker build → <image_repo>:<tag>
   3. docker push <image_repo>:<tag>
   4. POST <nanoku>/api/webhook/<app-name>
      body: {"tag":"<sha>","commit_message":"<msg>"}
      header: X-Hub-Signature-256: sha256=<hmac-hex>
   ↓
nanoku /api/webhook/{name}:
   1. 验签 HMAC-SHA256 with app.webhook_secret
   2. parse {tag, commit_message}
   3. DeployLock.TryAcquire(appID) → 拒同 app 重入
   4. goroutine executeWebhookDeploy(app, tag, msg):
      a. create Deploy(status=running, trigger=webhook, commit_sha=tag, msg=...)
      b. registry login（用 app 上的 registry creds；空 creds 走 daemon 默认）
      c. docker pull <app.image_repo>:<tag>
      d. 停旧 primary container + 起新容器（沿用现有 CreateAppContainer）
      e. regen caddyfile + reload
      f. Deploy status=success + finished_at
   5. 立刻返 202
```

## 3. nanoku 改动

### 3.1 Schema 改动

`internal/db/schema/app.go` 加一个字段：

```go
field.String("image_repo").
    Optional().
    Nillable().
    Comment("OCI image repository to pull on webhook deploy, e.g. ghcr.io/isaced/myapp. webhook payload's tag is appended as <repo>:<tag>.")
```

**含义**：当 webhook 触发 deploy，nanoku 用 `<app.image_repo>:<payload.tag>` pull image。

**手动 deploy**：沿用 `app.image` 字段（已有，spec 给完整 image:tag），不变。

`webhook_secret` 字段（v1 方案已定）保留。

### 3.2 Webhook payload（简化）

**只支持这两种内容**：
```json
{ "tag": "abc1234def", "commit_message": "fix: foo" }
```
或空 message：
```json
{ "tag": "abc1234def" }
```

不再解析 GitHub push event 大 JSON（GitHub Actions 已经把 commit 信息扁平化传过来了）。

### 3.3 HMAC 验签

跟 v1 一样：
- 缺 `X-Hub-Signature-256` → 401
- HMAC-SHA256(`webhook_secret`, body) 不匹配 → 401
- `hmac.Equal` constant-time

### 3.4 路由

```go
// main.go — 不走 BasicAuth
mux.HandleFunc("POST /api/webhook/{name}", handlers.Webhook)
```

放在 root mux，跳过 BasicAuth 中间件链。

### 3.5 DeployLock

新文件 `internal/api/deploy_lock.go`：单 app 串行，不同 app 并行。

```go
type DeployLock struct {
    mu sync.Mutex
    busy map[int]struct{}
}

func (l *DeployLock) TryAcquire(appID int) bool
func (l *DeployLock) Release(appID int)
```

### 3.6 新增 executeWebhookDeploy

在 `internal/api/apps.go` 加：

```go
func (h *Handlers) executeWebhookDeploy(ctx context.Context, a *db.App, tag string, msg string) error
```

逻辑如 2.4。**完整复制 DeployApp 的「pull → stop old → run new → regen caddy」核心**，但用 webhook 的 image_repo:tag 而不是 app.image。

### 3.7 删除的文件

- `internal/git/` 包（v1 方案里有，v2 不用）
- `internal/docker/build.go`（v1 方案里有，v2 不用）

### 3.8 Dockerfile 改动

v1 加了 git 二进制 copy，**v2 不需要**。Dockerfile 反而可以**瘦身**：把 v1 的 git copy 那行删掉。

镜像估算：**~36 MB**（distroless + go binary + docker CLI，无 git）。

## 4. UI 改动

### 4.1 App 表单（apps.tsx）— 加 webhook 配置区

在现有 RegistrySection 后加一个 `WebhookSection`：

```
┌──────────────────────────────────────────────────┐
│  🔔 GitHub webhook deploy                        │
│                                                  │
│  Image repository                                │
│  [ghcr.io/isaced/myapp________________]          │
│  Used as <repo>:<tag> when webhook fires.        │
│                                                  │
│  Branch  [main_____]                             │
│                                                  │
│  Webhook URL                                     │
│  ┌────────────────────────────────────────────┐  │
│  │ https://nanoku.example/api/webhook/myapp   │  │ [Copy]
│  └────────────────────────────────────────────┘  │
│                                                  │
│  Secret  ●●●●●●●●●●●●  [Rotate] [Copy]          │
│                                                  │
│  Save the secret when first shown — you won't   │
│  see it again. Use it as NANOKU_WEBHOOK_SECRET  │
│  in your GitHub repo.                            │
└──────────────────────────────────────────────────┘
```

### 4.2 AppDetail Overview tab

加 webhook 卡片（同上 URL + secret rotate）。

### 4.3 README 加 GitHub Actions 模板

```yaml
# .github/workflows/deploy.yml — copy into your app repo
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

UI 上加个「Copy workflow template」按钮 + README 加同款模板。

### 4.4 Deploys tab — 显示 commit 信息

扩展已存在 `DeployDTO`：在 `Deploys` 卡片里加 `commit_sha` (mono) + `commit_message`（前一行 truncate）。

Schema 已有 `commit_sha` / `commit_message` 字段，DTO 已有但 UI 没显示。加几行就行。

## 5. 错误处理

| 情况 | 行为 |
|---|---|
| `app.image_repo` 为空 | 404（app 没启用 webhook） |
| HMAC 缺 / 不匹配 | 401 + log warn |
| body 解析失败 | 400 |
| tag 格式非法（空、含特殊字符） | 400 |
| 同 app 正在 deploy | 202 + log info |
| registry login 失败 | Deploy=failed, error 包含 docker login stderr |
| docker pull 失败 | Deploy=failed, error 包含 pull stderr |
| docker run 失败 | Deploy=failed, error 含 run stderr |
| caddy reload 失败 | Deploy=failed（标 critical，因为上游不通） |
| 任意 panic | recover + Deploy=failed |

`tag` 校验：`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`（OCI tag 规则子集）

## 6. 测试

- `internal/api/webhook_test.go`：
  - 缺 secret → 401
  - HMAC 不匹配 → 401
  - 缺 image_repo → 404
  - 同 app busy → 202
  - 合法 payload → 202 + Deploy 创建（mock docker 客户端验证 pull/run 调用）
  - panic 恢复 → Deploy=failed, 进程不死
- `internal/api/deploy_lock_test.go`：并发 TryAcquire 单 app 只有 1 个 true
- 端到端（手动）：用 ngrok / 公网 nanoku + 公开 GitHub repo 真实跑一次

## 7. 实施步骤

1. `feat(schema): add image_repo field to App`（+ regen）
2. `feat(api): webhook handler with HMAC + DeployLock`
3. `feat(api): executeWebhookDeploy using app.image_repo + payload.tag`
4. `feat(api): webhook secret auto-generation on app create with repo_url`
5. `feat(ui): webhook section in app form + secret display modal`
6. `feat(ui): deploy commit info in deploys tab`
7. `docs: GitHub Actions workflow template in README + UI`
8. `docs: revise PROPOSAL-webhook to v2 (or archive v1)`

预计代码量：
- 后端 ~350 行（含测试） — 比 v1 少一半
- 前端 ~150 行
- docs ~80 行

## 8. 决策点（需要你确认）

### 决策 A：Compose 模式 + webhook 怎么处理？

- **A1（推荐）**：MVP 阶段 compose 模式不支持 webhook。app.deploy_method=compose 时 UI 隐藏 webhook 区域 + 提示「compose mode auto-deploy not supported in this version; manual deploy only」。把 webhook 当作 docker 模式的专属特性。
- **A2**：compose 模式也接受 webhook，payload 里多 `images: {service: image:tag}` 字段，nanoku 把 compose 文件里 image 替换后 `compose pull && up`。代码量 +100 行。
- **A3**：compose 模式自动 build（v1 旧方案的 compose 部分）。代码量 +300 行。

### 决策 B：Registry credential 怎么管理？

- **B1（推荐）**：app 上的 registry_username/password 已经是 schema 字段，webhook 流程复用现有 `WithRegistry` 登录。空 creds 时假设是匿名公开 registry（ghcr.io public image / DockerHub public）。
- **B2**：app 加 `registry_repo` 字段（指向 image_repo 所在 registry 域名），更精细。
- 倾向 B1，因为 schema 已支持，复用最小。

### 决策 C：webhook secret 跟谁配？

- **C1（推荐）**：app 表配 `webhook_secret`。用户在 app repo 的 GitHub Actions secrets/variables 里配 `NANOKU_WEBHOOK_SECRET`。
- **C2**：全局一个 webhook secret（所有 app 共享），更简单但粒度粗。
- 倾向 C1，隔离性更好。

### 决策 D：手动 deploy + image_repo 怎么处理？

- **D1（推荐）**：手动 deploy 走 app.image（已有，不变）。webhook deploy 走 app.image_repo + payload.tag。两个独立的概念。
- **D2**：把 image_repo 升级为「image 配置」总字段，手动 deploy 也用它（payload.tag 留空时用 app.branch 对应的 latest tag）。
- 倾向 D1，语义清晰，UI 上对用户来说「手动用 image，webhook 用 repo+tag」也好理解。

### 决策 E：是否保留 v1 旧方案（nanoku 自己 build）？

- **E1（推荐）**：完全删除 v1。v2 是唯一路径。git 包 / build.go / Dockerfile git copy 全部删掉。
- **E2**：保留为可选路径（config 开关 `NANOKU_BUILDER=external|self`）。
- 倾向 E1，符合「轻量化」哲学。

## 9. 范围外（明确不做）

- 多 provider（Gitea / GitLab）— 留 TODO
- 任何形式的 nanoku 本地 build
- Build cache / 多 stage 优化
- 多分支监听
- PR preview
- webhook secret 加密（与 registry_password 同 TODO）
- 自动 HTTPS（不同 issue）
- registry sidecar（让 nanoku 自己当 registry）— 引入复杂度和磁盘，违背轻量化

## 10. 验收

- [ ] 无 secret header → 401
- [ ] 错 secret → 401
- [ ] 合法 payload + image_repo 配置好 → 202，~10s 内 Deploy=running → success
- [ ] 同 app 同时两个 webhook → 第二个 202，Deploy 不重复
- [ ] `docker images` 看到 `<repo>:<tag>`
- [ ] Caddyfile 重生 + reload
- [ ] UI 显示 webhook URL + secret（创建后 modal + 详情卡）
- [ ] README + UI 提供 GitHub Actions workflow 模板
- [ ] docker build 镜像**不含** git 二进制（轻量化目标）
