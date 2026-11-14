# ProxyGate 代理网关

[![Release](https://github.com/lyxpm/proxygate/actions/workflows/release.yml/badge.svg)](https://github.com/lyxpm/proxygate/actions/workflows/release.yml)

轻量级、可视化、自动化的反向代理与网关。Go 后端内嵌 Vue 3 前端，单二进制运行，支持 Docker Compose、systemd 或源码启动。

## 核心特点

- 单进程、单二进制，不依赖 Node.js 运行时
- 站点、应用服务、节点、证书、日志、统计、拓扑可视化管理
- HTTP/1.1、HTTP/2、HTTPS、HTTP/3、WebSocket
- 轮询、加权轮询、主备、IP Hash、最少连接
- ACME HTTP-01 / DNS-01、自动续期、SNI 热加载
- 访问日志、审计日志、P95/P99、Prometheus 指标
- 配置版本与回滚、加密备份与原子恢复
- RBAC、TOTP、API Token、登录限流、管理面 IP 白名单

## Docker Compose

```bash
mkdir -p data
docker compose up -d
curl http://127.0.0.1:18080/api/v1/system/health
```

默认镜像：`ghcr.io/lyxpm/proxygate:latest`

本地构建：

```bash
docker build --build-arg VERSION=0.1.4 -t proxygate:latest .
PROXYGATE_IMAGE=proxygate:latest docker compose up -d
```

端口：

| 端口 | 用途 |
|---|---|
| `80` | HTTP 代理入口 |
| `443` | HTTPS 代理入口 |
| `443/udp` | HTTP/3 |
| `18080` | 管理 HTTP |
| `18443` | 管理 HTTPS |

容器以 root 运行，直接绑定以上端口；数据目录为 `./data:/data`。更新使用 `docker compose pull && docker compose up -d`，不会删除数据。

首次启动自动生成管理员密码，保存到 `data/initial-admin-password.txt`，首次成功登录后删除。

## systemd

构建：

```bash
cd web && pnpm install && pnpm build && cd ..
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w" -o proxygate-linux ./cmd/server
```

安装：

```bash
install -m 0755 proxygate-linux /usr/local/bin/proxygate-demo
mkdir -p /data/proxygate/data
install -m 0644 deployments/systemd/proxygate-demo.service \
  /etc/systemd/system/proxygate-demo.service
systemctl daemon-reload
systemctl enable --now proxygate-demo
```

更新前先备份 `data/`，替换二进制后重启服务。演示 unit 保留 root 运行，以兼容 CentOS 7 等旧 systemd 环境。

## 源码运行

```bash
cd web && pnpm install && pnpm build && cd ..
go run ./cmd/server
```

默认管理地址：`http://127.0.0.1:8080`。

## 首次登录

1. 打开 `http://127.0.0.1:8080`（Docker/systemd 为 `http://<服务器IP>:18080`）。
2. 用户名 `admin`。
3. 密码来自 `PG_ADMIN_PASSWORD`，未设置时读取 `data/initial-admin-password.txt`。
4. 首次成功登录后密码文件自动删除。
5. 创建应用服务 → 添加节点 → 新建站点 → 绑定域名和证书。

## 常用环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `PG_DATA_DIR` | `./data` | 数据目录 |
| `PG_ADMIN_LISTEN` | `127.0.0.1:8080` | 管理 HTTP 监听地址 |
| `PG_ADMIN_PASSWORD` | 空 | 首次管理员密码；留空自动生成 |
| `PG_SESSION_TTL` | `24h` | 会话有效期 |
| `PG_MASTER_KEY` | 自动生成 | base64 32 字节，用于加密敏感配置 |
| `PG_TRUSTED_PROXIES` | 空 | 可信代理 IP/CIDR |
| `PG_ADMIN_ALLOW_CIDRS` | 空 | 管理面 IP 白名单 |
| `PG_ALLOW_LOOPBACK_UPSTREAM` | `false` | 是否允许回环节点 |
| `PG_ACME_CA_FILE` | 空 | 私有 ACME CA 证书 |

完整示例见 [`configs/example.env`](configs/example.env)。


## 备份与恢复

- 系统设置中下载密码保护的 `.pgbak` 备份，使用 AES-256-GCM 加密；
- 包含数据库、配置、证书私钥、ACME 账户、用户与 Token；
- 不包含历史日志、会话、流量统计；
- 恢复前再次输入管理员密码；
- 恢复采用 staging + journal + 目录 rename，失败回滚；
- 旧版明文 ZIP 仍可恢复；
- 恢复会替换当前数据库和证书，请先备份。

## 安全说明

- 管理员密码 bcrypt；Session 仅存 SHA-256 哈希。
- Cookie HttpOnly + SameSite=Lax；HTTPS 下 Secure。
- 修改密码、禁用用户会吊销相关会话和 API Token。
- API Token 仅存哈希，支持 read / write 权限。
- DNS 凭据 AES-256-GCM 加密，API 不返回明文。
- 证书私钥独立文件，权限 0600，原子替换。
- 默认禁止回环节点；私网 RFC1918 地址允许。
- 访问日志中的 `token`、`access_token`、`api_key` 等参数脱敏。
- 日志下载仅管理员可用，并写入审计。
- 审计日志不记录密码、Token、私钥。

## License

MIT License。
