# Web UI

[English](webui.md) | **中文**

独立静态界面，通过 [管理 API](mgmt-api.zh-CN.md) 操作。不嵌入 `lynx-server` / `lynx-client` 主二进制。

| 二进制 | 默认监听 | 对接 |
|---|---|---|
| `lynx-web-server` | `127.0.0.1:9080` | server mgmt（常见 `:9090`） |
| `lynx-web-client` | `127.0.0.1:9081` | client mgmt（常见 `:9091`） |

```bash
lynx-web-server -listen 127.0.0.1:9080
lynx-web-client -listen 127.0.0.1:9081
```

浏览器打开 UI → **Settings** 填写 API 地址与 Bearer secret（与 `mgmt.secret` 一致），再使用概览 / 配置 / 服务 / 日志。

功能：状态、配置编辑（保存 / 保存并重启）、重启与升级、实时日志；客户端另有 reconnect 与订阅刷新。

静态资源：[`web/server`](../web/server)、[`web/client`](../web/client)。
