# Scripts

开发辅助脚本目录。

前端本地开发：

```bash
cd web
pnpm dev          # Vite dev server，/api 代理到 127.0.0.1:8080
```

后端本地运行：

```powershell
$env:PG_DATA_DIR="./data"
$env:PG_ADMIN_LISTEN="127.0.0.1:8080"
go run ./cmd/server
```

生产构建请使用根目录 `Makefile` 或 `Dockerfile`。
