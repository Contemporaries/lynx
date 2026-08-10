# Web UI

**English** | [中文](webui.zh-CN.md)

Separate static UIs that talk to the [management API](mgmt-api.md). They are **not** embedded in `lynx-server` / `lynx-client`.

| Binary | Default listen | Talks to |
|---|---|---|
| `lynx-web-server` | `127.0.0.1:9080` | server mgmt (often `:9090`) |
| `lynx-web-client` | `127.0.0.1:9081` | client mgmt (often `:9091`) |

```bash
lynx-web-server -listen 127.0.0.1:9080
lynx-web-client -listen 127.0.0.1:9081
```

Open the UI in a browser, open **Settings**, set API base + Bearer secret (must match `mgmt.secret`), then use Overview / Config / Service / Logs.

Pages: status overview, config editor (save / save & restart), restart & upgrade, live logs. Client UI also has reconnect and subscribe refresh.

Static sources also live under [`web/server`](../web/server) and [`web/client`](../web/client).
