# Management API

**English** | [中文](mgmt-api.zh-CN.md)

Optional HTTP API inside `lynx-server` / `lynx-client`. Disabled unless `mgmt.listen` is set. Auth: `Authorization: Bearer <secret>`.

## Config

```json
"mgmt": {
  "listen": "127.0.0.1:9090",
  "secret": "long-random-string",
  "cors_origin": "*",
  "allow_upgrade": true,
  "apply_restart": false
}
```

Bind to localhost by default. Do not expose to the public internet without TLS and a strong secret.

## Endpoints (`/api/v1`)

| Method | Path | Notes |
|---|---|---|
| GET | `/health` | No auth; liveness |
| GET | `/version` | Version + uptime |
| GET | `/status` | Role-specific status |
| GET/PUT/PATCH | `/config` | Redacted GET; write validates + backups under `/var/backups/lynx/` |
| GET | `/logs?level=info` | SSE log stream |
| POST | `/service/restart` | `systemctl restart` when possible, else re-exec |
| POST | `/service/reload` | Hot-apply log level; transport usually needs restart |
| GET/POST | `/upgrade` | Requires `allow_upgrade`; POST body `{"tag":"v2.3.0"}` or `{}` |
| GET | `/upgrade/status` | Progress |
| GET | `/clients` | Server only |
| POST | `/transport/reconnect` | Client only |
| POST | `/subscribe/refresh` | Client only; writes config, restart to apply certs |

Masked secrets on GET (`***` / `***PEM***`) are preserved on PUT/PATCH when left unchanged.
