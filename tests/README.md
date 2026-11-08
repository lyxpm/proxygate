# Tests

Integration and end-to-end tests live next to the code they exercise:

- `internal/proxy/*_test.go`：多域名路由、请求头、热更新、健康检查、TLS/SNI、WebSocket。
- `internal/app/app_test.go`：HTTP API → SQLite → 代理引擎 → 仪表盘统计 → 配置回滚。
- `internal/site`、`internal/upstream`、`internal/auth`、`internal/statistics`、`internal/storage`：业务规则与持久化。

运行：

```bash
go test ./...
```

本目录预留给跨模块的 shell/Compose 端到端测试脚本。
