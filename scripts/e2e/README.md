# nanoku E2E 测试

通过 **真实启动 nanoku + curl** 串联通测核心业务链路,验证:
- HTTP 路由 / Session 鉴权 / CORS / 限流
- Apps / Sites 的 CRUD 与状态机
- 敏感字段(registry 密码、trigger token、env var)加密存盘
- 真实 docker 容器生命周期(deploy / start / stop / restart)
- **caddy 反代真通**(创建 app → 部署 → 绑 site → 外部 curl 拿响应)

每个 `test-*.sh` 是独立 case,失败时打印上下文,不互相阻塞。

## 依赖

| 工具 | 用途 | 装 |
|---|---|---|
| `go` ≥ 1.26 | `go run .` 启服务 | 项目已要 |
| `curl` | HTTP 客户端 | 系统自带 |
| `jq` | 解析 JSON 响应 | `brew install jq` |
| `docker` | 真链路(deploy / caddy / proxy) | Docker Desktop |
| `openssl` | 生成 SECRET_KEY | 系统自带 |

## 跑法

```bash
# 跑全部
make test-e2e
# 或
bash scripts/e2e/run-all.sh

# 跑子集
bash scripts/e2e/run-all.sh 'test-1*'      # 只跑 auth
bash scripts/e2e/run-all.sh 'test-70*'     # 只跑 caddy 反代
```

输出:
```
═══════════════════════════════════════════════════
  Running 13 E2E test files
═══════════════════════════════════════════════════

▶ test-00-health.sh
── /healthz bypass auth ──
✓ HTTP 200  /healthz with no cookie
✓ /api/me with no cookie returns 401
...
  ✓ test-00-health.sh PASSED (2s)

...
═══════════════════════════════════════════════════
  E2E Summary
═══════════════════════════════════════════════════
  Test files: 13
  Passed:     13
  Failed:     0
```

## 覆盖矩阵

| Case | 文件 | 关键验证点 |
|---|---|---|
| 健康检查 | `test-00-health.sh` | `/healthz` 不走 auth;未登录 `/api/me` 401;CORS 预检 |
| 鉴权 | `test-10-auth.sh` | 错密码 401;限流 429;改密旧 cookie 失效;登出 |
| 站点 | `test-20-sites.sh` | CRUD + toggle;非法 name 400 |
| Apps CRUD | `test-30-apps-crud.sh` | docker 模式必填;compose 模式分支;ClearRegistry |
| Apps env | `test-31-apps-env.sh` | PUT 覆盖语义;DB 存的是 `enc:` 前缀 |
| Apps volumes | `test-32-apps-volumes.sh` | PUT 覆盖;顺序按 ID |
| Trigger token | `test-33-apps-trigger.sh` | rotate 一次性;List/Get 不返回;DB 加密 |
| Compose 模式 | `test-34-apps-compose.sh` | 内联 YAML;composeFile 自动生成 |
| System | `test-40-system.sh` | status / caddyfile / dashboard / cleanup |
| HTTP trigger | `test-50-trigger.sh` | 鉴权失败/成功;deploy lock;deployId 返回 |
| Deploy | `test-60-deploy.sh` | 拉真镜像;容器真起;deploy history |
| Lifecycle | `test-61-lifecycle.sh` | start/stop/restart 真容器 |
| Caddy 反代 | `test-70-proxy.sh` | 创建 site → caddy reload → curl 拿容器响应 |

## 资源隔离

E2E 跑在你机器上时**不会污染**本地 dev:

- 临时目录:`$TMPDIR/nanoku-e2e.XXXXXX`
- 临时 DB:`<workdir>/nanoku.db`
- 临时 admin 账号:`admin` / `e2e-test-<pid>-pass`
- caddy 容器:`nanoku-e2e-caddy`(不是 `nanoku-caddy`)
- caddy 网络:`nanoku-e2e-net`
- caddy 卷:`nanoku-e2e-data`
- 监听端口:`:18080`(不是 `:8080`)
- 临时 app 容器:`nanoku-<testname>`,有 `nanoku.managed=true` label

`teardown.sh` 全清,工作目录直接 `rm -rf`。

## 调试

服务 log 在 `E2E_WORKDIR/server.log`,case 失败时:
```bash
tail -100 /tmp/nanoku-e2e.*/server.log
```

单个 case 重跑:
```bash
bash scripts/e2e/setup.sh    # 启服务
bash scripts/e2e/test-30-apps-crud.sh  # 跑单 case
bash scripts/e2e/teardown.sh # 清
```

## CI

`.github/workflows/e2e.yml` 用 `ubuntu-latest` runner(自带 docker),直接跑 `make test-e2e`。详见 workflow 文件。

## 已知环境问题

### OrbStack: caddy 容器被 SIGKILL

在 macOS + OrbStack 环境下,nanoku 自己 ensure 起来的 caddy 容器会
在启动后约 1 秒被 SIGKILL(exit code 137),docker events 里能看到但查
不到明确的 killer。caddy 镜像本身没问题(`docker run` 直接跑就稳)。

猜测跟 OrbStack 的容器管理策略有关(可能与 bind mount、netd、
systemd-style daemon 行为冲突),没在 Linux / Docker Desktop 上复现。

**当前 E2E 的处理**:
- `setup.sh` 会重试 30 秒尝试拉起 caddy。失败时打印 warning,
  test 仍继续。
- 依赖 caddy 容器在跑(`/api/sites*`、`/api/apps/{id}/deployments`、
  caddy 反代测试)的 case 用 `require_caddy` helper,caddy 不在就
  **自动 SKIP**(不 fail),所以 caddy 受影响的环境跑 `run-all.sh`
  不会全红,只是少几个 case。
- Linux CI / Docker Desktop 上没有这个问题,所有 case 正常跑。

**用户能做什么**:
- Linux 上跑完整测试
- macOS + Docker Desktop(不是 OrbStack)上跑完整测试
- macOS + OrbStack 上接受部分 case skip(主要是反代相关)

调试时如果 caddy 起不来,直接看 `cat $E2E_WORKDIR/server.log`,
里面会有 nanoku 启动时 caddy ensure 的日志。

