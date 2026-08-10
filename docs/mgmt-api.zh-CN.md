# 管理 API

[English](mgmt-api.md) | **中文**

`lynx-server` / `lynx-client` 可选进程内 HTTP API。未配置 `mgmt.listen` 时关闭。鉴权：`Authorization: Bearer <secret>`。

## 配置

```json
"mgmt": {
  "listen": "127.0.0.1:9090",
  "secret": "long-random-string",
  "cors_origin": "*",
  "allow_upgrade": true,
  "apply_restart": false
}
```

默认只绑本机。不要在无 TLS、弱密钥的情况下对公网开放。

## 端点（`/api/v1`）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/health` | 无鉴权存活探测 |
| GET | `/version` | 版本与 uptime |
| GET | `/status` | 角色相关状态 |
| GET/PUT/PATCH | `/config` | GET 脱敏；写入前备份到 `/var/backups/lynx/` |
| GET | `/logs?level=info` | SSE 日志流 |
| POST | `/service/restart` | 优先 `systemctl restart`，否则自重启 |
| POST | `/service/reload` | 热加载日志级别等 |
| GET/POST | `/upgrade` | 需 `allow_upgrade` |
| GET | `/upgrade/status` | 升级进度 |
| GET | `/clients` | 仅 server |
| POST | `/transport/reconnect` | 仅 client |
| POST | `/subscribe/refresh` | 仅 client |

GET 中的打码密钥（`***`）在 PUT/PATCH 未改写时会保留原值。
