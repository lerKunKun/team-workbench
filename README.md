# 团队项目工作台 · Team Workbench

团队交付与项目评估工作台：查看每周交付、负责人和阻塞，管理项目权限与立项决策，从外部 Roadmap 导入审核后的摘要，并编写版本化 `plan.md`。

| 路径 | 内容 |
| --- | --- |
| `team-workbench/web` | Next.js、shadcn/ui：项目看板、管理后台、评估与计划 |
| `team-workbench/api` | Go、Casbin、PostgreSQL：账号、项目权限、审计、Roadmap 接入 |
| `rd-automation/gateway` | Go：钉钉 Stream、草稿确认、外部项目工具接入 |
| `rd-automation/n8n/workflows` | n8n 合并流程及旧版模板；只包含节点和示例凭据引用，不含凭据值 |

## 本地体验

需要 Docker Compose 和 OpenSSL。以下步骤从仓库根目录执行，适用于新实例。默认使用模拟项目数据，不会调用生产服务。已存在 `.env` 时不要覆盖它。

```sh
cd team-workbench
(umask 077; set -C; cat .env.example > .env)
# 把 .env 内空的 WORKBENCH_DB_PASSWORD 填为 `openssl rand -hex 32` 的输出。
docker compose up -d --build --wait
```

没有默认管理员密码。在当前终端用 `read -rs` 或密码管理器安全设置 `WB_ADMIN_PASSWORD`（至少 12 位），不要把密码写进命令历史，再初始化空数据库：

```sh
printf '%s' "$WB_ADMIN_PASSWORD" | docker compose exec -T api sh -eu -c '
  umask 077; f=$(mktemp); trap '\''rm -f "$f"'\'' EXIT
  cat > "$f"; WORKBENCH_BOOTSTRAP_PASSWORD_FILE="$f" workbench-api bootstrap-admin admin
'
unset WB_ADMIN_PASSWORD
```

浏览 [工作台](http://localhost:3111/login)。项目列表在 `/projects`，系统管理在 `/admin/users`，内部评估在 `/roadmap/manage`。

## 对接外部系统

- **部署域名**：所有 `*.example.com` 都是保留示例域名。配置自己的 HTTPS 反代、`WORKBENCH_ORIGIN` 和可选的 `WORKBENCH_ADMIN_ORIGIN`。替换前端登录外链、Roadmap 外链和 `api/internal/roadmap/bridge.go` 中的 Roadmap 管理端目标，再构建。管理后台与工作台使用独立 Cookie；不要公开 `/internal/` 或 n8n Webhook。
- **Linear 和模型**：在私有 `.env` 配置独立 Linear 沙箱工作区的项目和受限 API Key，再切换 `WORKBENCH_MODE=linear`。立项审批只记录本地决定。模型可选；未配置时仍可人工保存、下载 Markdown。
- **钉钉与 n8n**：在 `rd-automation` 下运行 `sh scripts/init-env.sh` 生成本地密钥，再启动 Compose。需要互通时，两端都加上各自的 `ops/wb-bridge.compose.yml`，提前创建同名私有网络 `rd-workbench-dm`。导入 `RD-Project-Workbench.json`，在 n8n 中绑定自己的 Header Auth 凭据。合并流程和旧模板不能同时发布，以免生产 Webhook 路径冲突。默认不启用钉钉 Stream、群聊或定时晨报；先验证试点身份、模板与独占消费者。
- **外部 Roadmap**：该仓库提供工作台的 SSO 与反馈接口、项目映射和 n8n 分支；外部看板和身份供应端需单独部署。开启前核对双方身份绑定和项目映射，并配置独立的 SSO、分发、Webhook 和导入密钥。没有映射时不会导入。内部决策不会自动回写到公开 Roadmap。

## 测试与数据说明

- 在 `team-workbench/api` 与 `rd-automation/gateway` 分别运行 `go test ./...`、`go vet ./...`。
- 在 `team-workbench/web` 运行 `pnpm install --frozen-lockfile`、`pnpm lint`、`pnpm build` 和 `pnpm exec playwright test`。
- 在 `rd-automation` 运行 `python3 scripts/test-roadmap-workflow.py`。
- 数据库集成测试只使用独立的可丢弃测试库；不要将测试数据库环境变量指向工作库。
- 仓库为脱敏源码快照，不含生产环境变量、账号数据、部署记录、备份或内部运维文档。`OFL.txt` 随 Inter 字体保留；本仓库未另行声明软件许可证。
