# ProxyGate 代理网关

[![Release](https://github.com/lyxpm/proxygate/actions/workflows/release.yml/badge.svg)](https://github.com/lyxpm/proxygate/actions/workflows/release.yml)

轻量级、可视化、自动化的反向代理与网关。后端使用 Go 编写，前端使用 Vue 3，单进程运行，支持单二进制或单容器部署。

## 项目介绍

ProxyGate 面向需要自行管理域名、证书和上游服务的场景，把 Nginx / Caddy 常见的反向代理能力集中到一个可视化管理后台中。创建应用服务、添加站点、上传或申请证书后即可对外提供 HTTP/HTTPS 服务，配置保存后热更新，无需手工修改配置文件或执行 reload。


### 核心特点

- **单进程、单二进制**：Go 后端内嵌前端静态资源，不依赖 Node.js 运行时。
- **可视化管理**：站点、应用服务、节点、证书、日志、统计、拓扑均在 Web 后台完成。
- **热更新**：站点和应用配置保存后立即生效；管理 HTTPS 和代理监听可独立重启。
- **多协议入口**：HTTP/1.1、HTTP/2、HTTPS、HTTP/3（QUIC）、WebSocket。
- **多节点负载均衡**：轮询、加权轮询、主备、IP Hash、最少连接，支持主动/被动健康检查。
- **证书自动化**：PEM 上传、ACME HTTP-01/DNS-01、自动续期、SNI 热加载。
- **可观测性**：访问日志、系统日志、审计日志、站点流量统计、P95/P99、Prometheus 指标。
- **配置版本与回滚**：每次成功应用生成配置快照，支持保留上限、回滚和删除。
- **备份恢复**：配置级 zip 备份，恢复后自动重启并重置为配置版本 1。
- **安全默认值**：Session、RBAC、TOTP、登录限流、API Token、管理面 IP 白名单、凭据加密。

### 运行架构

```text
浏览器
  │  HTTP/HTTPS 管理面（18080 / 18443）
  ▼
Vue 3 管理后台
  │  REST API
  ▼
Go API / 业务层
  ├── SQLite（WAL）
  ├── 证书与 ACME 文件
  ├── 访问日志 JSONL
  └── 运行时配置
          │
          ▼
   Go 反向代理引擎
   ├── 80 / 443（HTTP/HTTPS/HTTP3）
   ├── 站点路由
   ├── 应用服务与负载均衡
   └── 上游节点
```

## 功能概览

| 模块 | 能力 |
|---|---|
| 代理引擎 | 多域名/多端口、HTTP/1.1、HTTP/2、HTTPS、HTTP/3、WebSocket、路径重写、请求/响应头、超时、请求体限制 |
| 站点管理 | 创建、编辑、删除、启停、复制、域名冲突检测、证书校验、JSON 导入/导出 |
| 应用服务 | 多节点、轮询、加权轮询、主备、IP Hash、最少连接、权重/优先级、主动/被动健康检查 |
| 真实客户端 IP | 站点级“传递真实访问 IP”开关，默认开启，向后端写入 `X-Forwarded-For`；代理不写入 `X-Real-IP`，并清理客户端伪造头 |
| 证书 | PEM 上传、ACME HTTP-01/DNS-01、SNI 热加载、自动续期、续期日志、到期提醒、证书查看/下载 |
| DNS 服务商 | Cloudflare、阿里云、腾讯云 DNSPod、华为云；凭据 AES-256-GCM 加密 |
| 日志 | 访问日志 JSONL、按日切分、历史查询、保留/容量清理、实时 SSE；系统日志、审计日志 |
| 统计 | 分钟聚合、请求/流量/状态码/耗时、P95/P99、站点排行、TOP IP/URL、CPU/内存 |
| 拓扑 | 横/竖布局、节点对齐、QPS 流量线宽、节点详情与 60 秒曲线 |
| 配置版本 | 每次成功应用生成快照，支持保留上限、回滚、单条/批量删除；备份不导出历史版本 |
| 备份恢复 | 一致性 zip 备份，恢复后自动重启，配置版本重置为 1 |
| 认证与权限 | 管理员/只读用户、bcrypt、Session、登录锁定、空闲超时、密码策略、会话吊销、API Token、TOTP |
| 部署 | Docker Compose、systemd 单二进制、源码运行 |

## 技术栈

- 后端：Go 1.27、`net/http`、`httputil.ReverseProxy`、chi、GORM、纯 Go SQLite、lego v4、Prometheus client。
- 前端：Vue 3、TypeScript、Vite、Element Plus、Pinia、Vue Router、ECharts、Vue Flow。
- 存储：SQLite（WAL）、证书/私钥独立文件、访问日志 JSONL。
- 部署：单二进制、systemd、Docker Compose。

## 端口与数据目录

| 端口 | 用途 |
|---|---|
| `80` | HTTP 代理入口 |
| `443` | HTTPS 代理入口 |
| `443/udp` | HTTP/3（QUIC） |
| `18080` | 管理后台 HTTP |
| `18443` | 管理后台 HTTPS（启用后生效） |

默认数据目录：

```text
data/
├── database/proxygate.db     # SQLite 数据库
├── certificates/             # 站点证书与私钥
├── acme/                     # ACME 账户
├── config/                   # master key、待恢复备份
├── logs/                     # 访问/系统日志
├── backups/                  # 恢复归档
└── initial-admin-password.txt # 首次启动自动生成，改密后删除
```

## 部署方式一：Docker Compose

适合新环境快速部署。前置：Docker 20+、Docker Compose v2。

发布镜像由 GitHub Actions 自动构建并推送到 GHCR：

```bash
# 1. 拉取发布镜像
docker pull ghcr.io/lyxpm/proxygate:latest

# 2. 准备数据目录并启动
mkdir -p data
docker compose up -d

# 3. 查看状态
docker compose ps
curl http://127.0.0.1:18080/api/v1/system/health
```

如需本地构建：

```bash
docker build --build-arg VERSION=0.1.2 -t proxygate:latest .
PROXYGATE_IMAGE=proxygate:latest docker compose up -d
```

Compose 端口映射：

- `80`、`443`、`443/udp`：代理入口；
- `18080`：管理后台 HTTP；
- `18443`：管理后台 HTTPS；
- `./data:/data`：数据持久化，容器升级不丢失。

说明：

- 默认使用 `ghcr.io/lyxpm/proxygate:latest`；可用 `PROXYGATE_IMAGE` 覆盖为指定版本或本地镜像。
- 镜像默认以 `root` 运行，直接绑定 80/443。
- 无需 `.env` 和预置密码；首次启动会生成管理员密码，写入 `data/initial-admin-password.txt`。
- 更新镜像后执行 `docker compose pull && docker compose up -d`；`docker compose down` 不会删除 `./data`。
- 如使用私有仓库镜像，可将 `PROXYGATE_IMAGE` 设置为实际镜像地址。

查看日志：

```bash
docker compose logs -f proxygate
```

## 部署方式二：systemd 直接运行

适合不使用容器、或需要直接管理主机的场景。

### 1. 构建 Linux 二进制

前置：Go 1.27+、Node.js 22+、pnpm 10+。

```bash
# 前端资源会被 Go embed 进二进制
cd web
pnpm install
pnpm build
cd ..

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w" \
  -o proxygate-linux ./cmd/server
```

### 2. 安装并启动

```bash
install -m 0755 proxygate-linux /usr/local/bin/proxygate-demo
mkdir -p /data/proxygate/data

install -m 0644 deployments/systemd/proxygate-demo.service \
  /etc/systemd/system/proxygate-demo.service

systemctl daemon-reload
systemctl enable --now proxygate-demo
systemctl status proxygate-demo
```

`deployments/systemd/proxygate-demo.service` 默认配置：

```ini
WorkingDirectory=/data/proxygate
Environment=PG_DATA_DIR=/data/proxygate/data
Environment=PG_ADMIN_LISTEN=0.0.0.0:18080
ExecStart=/usr/local/bin/proxygate-demo
Restart=always
RestartSec=2
```

按实际路径调整后执行：

```bash
systemctl daemon-reload
systemctl restart proxygate-demo
journalctl -u proxygate-demo -f
```

### 3. 更新

```bash
systemctl stop proxygate-demo
tar -czf /data/proxygate/backup-$(date +%Y%m%d-%H%M%S).tar.gz \
  -C /data/proxygate data
install -m 0755 proxygate-linux /usr/local/bin/proxygate-demo
systemctl start proxygate-demo
```

`Restart=always` 也是“恢复备份后自动重启”的前提；如果直接手工运行二进制，需要自行提供进程守护。

## 部署方式三：源码运行（开发）

```bash
# 终端 1：前端构建
cd web
pnpm install
pnpm build
cd ..

# 终端 2：启动后端
go run ./cmd/server
```

默认管理地址：<http://127.0.0.1:8080>。如需修改监听地址：

```bash
PG_ADMIN_LISTEN=0.0.0.0:18080 \
PG_DATA_DIR=./data \
go run ./cmd/server
```

## 首次启动与登录

1. 打开管理后台：
   - 源码运行：`http://127.0.0.1:8080`
   - Docker / systemd：`http://<服务器IP>:18080`
2. 使用 `admin` 登录。
3. 首次密码来源：
   - 设置了 `PG_ADMIN_PASSWORD`：使用该密码；
   - 未设置：自动生成，保存到 `data/initial-admin-password.txt`，日志中也会提示路径；
   - 首次修改密码后，该文件自动删除。
4. 建议立即修改密码，并在“系统设置”中配置管理 HTTPS、会话策略和登录限制。
5. 创建应用服务 → 添加节点 → 新建站点绑定域名和证书 → 访问域名。

启用管理 HTTPS 后，HTTP 管理端口只保留健康检查并跳转 HTTPS；关闭后恢复完整的 HTTP 登录能力。

## 常用环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `PG_DATA_DIR` | `./data` | 数据目录（数据库、证书、日志、主密钥） |
| `PG_ADMIN_LISTEN` | `127.0.0.1:8080` | 管理后台 HTTP 监听地址；Docker 镜像默认 `0.0.0.0:18080` |
| `PG_ADMIN_PASSWORD` | 空 | 首次启动的管理员密码；留空自动生成 |
| `PG_SESSION_TTL` | `24h` | 会话有效期 |
| `PG_MASTER_KEY` | 自动生成 | base64 32 字节，用于加密 DNS 凭据等敏感数据 |
| `PG_TRUSTED_PROXIES` | 空 | 可信代理 IP/CIDR，逗号分隔 |
| `PG_ADMIN_ALLOW_CIDRS` | 空 | 管理后台 IP 白名单，留空允许所有来源 |
| `PG_ACME_CA_FILE` | 空 | 私有 ACME CA 的 PEM 文件（step-ca / Pebble） |
| `PG_DEV` | `false` | 输出 SQL 调试日志 |

完整示例见 [`configs/example.env`](configs/example.env)。

## 备份与恢复

- 管理后台“系统设置 → 备份与恢复”可下载一致性 zip 备份。
- 备份包含：数据库、站点/应用/证书配置、证书私钥、ACME 账户、系统设置、用户与 API Token、DNS 凭据。
- 备份不包含：历史配置版本、访问/系统/审计/证书日志、会话、流量统计。
- 恢复前会要求再次输入管理员密码；恢复包会校验路径、文件类型、大小和 SQLite 完整性。
- 恢复采用“暂存 + 自动重启”：
  - 校验通过后服务进程退出；
  - systemd `Restart=always` 或 Docker `restart: unless-stopped` 拉起新进程；
  - 启动时应用备份数据；
  - 历史配置版本被清空，首次应用生成配置版本 1。
- 恢复会替换当前数据库和证书，请先下载当前备份。
- 自动重启依赖进程守护；手工前台运行时不会自动拉起。

## 健康检查与运维

健康检查：

```bash
curl http://127.0.0.1:18080/api/v1/system/health
```

返回：

```json
{"code":0,"message":"ok","data":{"database":true,"proxy":true,"status":"ok"}}
```

Docker：

```bash
docker compose ps
docker compose logs -f proxygate
docker inspect --format '{{.State.Health.Status}}' proxygate
```

systemd：

```bash
systemctl status proxygate-demo
journalctl -u proxygate-demo -f
```

Prometheus 指标默认暴露在管理面 `/api/v1/system/metrics`。

## 安全说明

- 管理员密码 bcrypt（cost 12）；Session 仅存 SHA-256 哈希；Cookie HttpOnly + SameSite=Lax。
- 站点支持 IP/CIDR 允许或拒绝列表（`ipMode` / `ipList`）。
- API Token 仅存哈希，支持 read / write 权限，明文只在创建时显示一次。
- 登录失败锁定、Session 空闲超时、密码复杂度策略可在系统设置调整。
- 修改密码后所有会话失效；启用 TOTP 会吊销其他会话。
- DNS API 凭据 AES-256-GCM 加密，API 不返回明文。
- 证书私钥独立文件，权限 0600；更新使用临时文件 + rename 原子替换。
- 默认禁止上游节点使用链路本地/元数据地址；私网地址可通过策略关闭。
- 管理 API 变更请求校验 `Origin`；仅信任 `PG_TRUSTED_PROXIES` 的转发头。
- 审计日志不记录密码、Token、私钥。

## 测试

```bash
go test ./...
go vet ./...
```

前端构建与 E2E：

```bash
cd web
pnpm build

PG_E2E_BASE_URL=https://your-host:18443 \
PG_E2E_USER=admin \
PG_E2E_PASSWORD='你的密码' \
pnpm exec playwright test
```

覆盖范围包括：

- 轮询、加权轮询、主备、全节点故障；
- 多域名路由、请求/响应头、热更新、未知域名 404；
- 健康检查摘除/恢复、TLS/SNI、WebSocket、HTTP/2、HTTP/3；
- 站点校验、域名冲突、证书覆盖；
- 认证、登录限流、TOTP、RBAC；
- SQLite 迁移、流量 UPSERT、统计聚合、P95/P99；
- 备份内容、恢复后版本重置、自动重启；
- 前端登录、仪表盘、核心导航、图表回归、XFF 开关布局。

## 项目结构

```text
cmd/server/                 # 程序入口
internal/
  app/                      # 依赖装配、生命周期、运行时配置
  api/                      # chi 路由、中间件、REST handler
  proxy/                    # 代理引擎、负载均衡、健康检查
  site/                     # 站点业务规则
  application/              # 应用服务与节点
  certificate/              # 证书、ACME、续期、DNS 适配
  log/                      # 访问日志、系统日志、SSE
  statistics/               # 内存聚合 + SQLite 分钟桶
  auth/                     # 认证、Session、限流
  storage/                  # SQLite、迁移、仓储
  config/  secure/          # 环境配置、AES-GCM
migrations/                 # 内嵌 SQL 迁移
web/                        # Vue 3 管理界面
deployments/
  docker/                   # 运行镜像辅助
  systemd/                  # systemd 单元
```

## License

本项目采用 MIT License。
