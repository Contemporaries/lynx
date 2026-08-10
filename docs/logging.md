# Logging

**English** | [中文](logging.zh-CN.md)

Lynx emits **traffic-first** process logs (who connected, which path, bytes) plus operational events. Levels: `debug` / `info` (default) / `warn` / `error`. Setting a level prints that level and more severe.

## Config

```json
"log": { "level": "info" }
```

CLI override: `-log-level debug`.

## What you see at `info`

```text
flow open id=12 net=tcp target=example.com:443 path=wss via=wss://…
flow close id=12 target=example.com:443 ok=true up=8192 down=102400 duration=3.2s path=wss
udp associate open path=direct via=…
udp associate close packets_up=4 packets_down=4 bytes_up=320 bytes_down=512 duration=1.1s
auto: path changed from=wss to=direct …
```

Use `debug` for per-datagram destinations and handshake detail. `warn` covers private-network denials, rate limits, unauthorized clients, and WSS→direct fallback.

Live stream: management API `GET /api/v1/logs?level=info` (SSE). See [mgmt-api.md](mgmt-api.md).
