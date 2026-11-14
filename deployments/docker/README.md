# Docker 部署

根目录的 `Dockerfile` 与 `docker-compose.yml` 是推荐入口：

```bash
cp configs/example.env .env
docker compose up -d --build
```

说明：

- 前端在 Node 构建阶段生成 `web/dist`，随后由 Go `embed` 打进单二进制。
- 容器以 root 用户运行，直接绑定代理 80/443 和管理 18080/18443，不新增普通用户。
- 数据目录 `./data` 绑定挂载到容器 `/data`，包含 SQLite、证书、ACME 账户、配置与主密钥。
- 管理端口映射到宿主机所有地址 `18080`（HTTPS `18443`）；建议配合防火墙和 HTTPS。
- 升级：`docker compose up -d`，数据目录保持不变。

> 已在本机 Docker Desktop/WSL2 完成构建与启动验证；CentOS 7.9 已用 Compose 部署。若 Docker Hub 不可直连，请先配置镜像加速或预拉取 `node:22-slim`、`golang:1.27-alpine`、`alpine:3.22`。
