# Cloudflare 配置

[English](cloudflare.md) | **中文**

说明 Lynx 如何用 Cloudflare Tunnel 承载 WSS 数据面、DNS、可选 Access，以及连接如何端到端加密。

## Cloudflare 的角色

| 流量 | 是否经过 Cloudflare |
|---|---|
| 数据面 WSS（`/_lynx/v1/connect`） | **是** — Tunnel → origin `127.0.0.1:8080` |
| 订阅（`/_lynx/v1/subscribe/`） | **否** — 订阅域名上的 nginx（HTTPS 443） |
| 直连 mTLS（`:8443`） | **否** — 直连域名应为 **DNS only**（灰云） |

Cloudflare 终止公网 HTTPS/WSS。Tunnel 之后 Lynx 仍做 **内层 TLS 1.3 + 每设备 mTLS**，CDN 无法读取代理明文（目标与载荷）。

## 前置条件

1. 已在 Cloudflare 托管的 CDN 主机名（如 `cdn.example.com`）
2. 能在浏览器完成 `cloudflared tunnel login`（证书通常在 `/root/.cloudflared/cert.pem`）
3. 可选：若用 Access 保护 CDN 主机名，准备 Service Token

一键向导（`lynx-wizard` / `install.sh`）会安装 `cloudflared`、建 Tunnel、写配置并启用 `lynx-cloudflared`。下文与向导行为一致，便于手工排查。

## 向导流程（摘要）

首次部署会询问：CDN 域名、订阅域名、是否直连、设备名、是否已启用 CF Access Service Token。确认后：

1. 从 Cloudflare 软件源安装 `cloudflared`
2. 如需要则 `cloudflared tunnel login`
3. 创建 Tunnel（名类似 `lynx-<host>-<timestamp>`）
4. 凭据放到 `/etc/lynx/cloudflared/<uuid>.json`
5. DNS：`cloudflared tunnel route dns --overwrite-dns <uuid> <cdn_host>`
6. 写入 `/etc/lynx/cloudflared.yml` 并启动 `lynx-cloudflared`

## Tunnel 配置

向导默认路径：`/etc/lynx/cloudflared.yml`

```yaml
tunnel: <tunnel-uuid>
credentials-file: /etc/lynx/cloudflared/<tunnel-uuid>.json
protocol: quic
ingress:
  - hostname: cdn.example.com
    path: ^/_lynx/v1/connect$
    service: http://127.0.0.1:8080
  - service: http_status:404
```

仓库样例：[deploy/cloudflared-config.yml](../deploy/cloudflared-config.yml)。

要点：

- **只代理 connect 路径**；该主机名上其余路径由 Tunnel 返回 404
- 订阅**不要**走这条 ingress；用 nginx → `127.0.0.1:8080`
- Origin 为本机明文 HTTP；公网 TLS 在 CF 边缘，Lynx 内层再加密一次

systemd：`lynx-cloudflared` 运行：

```text
cloudflared --no-autoupdate --config /etc/lynx/cloudflared.yml tunnel run
```

## DNS

| 主机名 | 建议 Cloudflare 代理 | 用途 |
|---|---|---|
| `cdn.example.com` | **Proxied**（橙云）经 Tunnel CNAME | WSS |
| `subscribe.example.com` | 按你的证书方案 | nginx 订阅 |
| `direct.example.com` | **DNS only**（灰云） | 直连 mTLS 到 `:8443` |

若 `tunnel route dns` 失败，删除 CDN 名冲突的 A/AAAA/CNAME 后重跑：

```bash
sudo cloudflared tunnel route dns <tunnel-uuid> cdn.example.com
```

部署完成会打印：

```text
Cloudflare 代理入口：wss://cdn.example.com/_lynx/v1/connect
```

## WSS 连接路径

```text
应用 → Lynx 客户端（SOCKS5/HTTP）
        → TLS 到 Cloudflare（cdn.example.com:443）
        → 可选 CF Access 请求头
        → WebSocket 升级 /_lynx/v1/connect
        → Tunnel → http://127.0.0.1:8080
        → Lynx 服务端接受 WS
        → 内层 TLS 1.3 + 客户端证书（SNI lynx.internal）
        → 多路复用代理流
```

客户端字段（订阅 / 向导包之后）示例：

```json
{
  "mode": "wss",
  "ws_url": "wss://cdn.example.com/_lynx/v1/connect",
  "ws_inner_server_name": "lynx.internal",
  "cf_access_client_id": "可选",
  "cf_access_client_secret": "可选"
}
```

启用直连时，包里通常是 `"mode": "auto"` 并带 `direct_addr`。

### 超时

- 直连 dial：约 5s
- WSS dial：约 20s
- `auto`：先直连；失败日志  
  `direct TLS unavailable, falling back to Cloudflare WSS` 后再 WSS

## Cloudflare Access（可选）

用 Access 应用保护 `cdn.example.com`，策略允许 **Service Token**：

1. Zero Trust → Access → Service Auth → 创建 Service Token（Client ID / Secret）
2. 为 CDN 主机名（或 connect 路径）建应用，策略：Service Token
3. 向导询问时填入，或写入 `client.json`：

```json
"cf_access_client_id": "...",
"cf_access_client_secret": "..."
```

客户端在 WSS 握手发送：

```http
CF-Access-Client-Id: ...
CF-Access-Client-Secret: ...
```

**Lynx 服务端不校验 Access**；由 Cloudflare 边缘校验。订阅 token 与设备证书是另一套机制。

Access 凭据一般由向导写入**客户端包**；订阅 HTTP API **不会**下发这对密钥。

## 验证

```bash
sudo systemctl status lynx-server lynx-cloudflared
curl -sS http://127.0.0.1:8080/_lynx/v1/version
curl -sSI https://cdn.example.com/_lynx/v1/connect
```

客户端连接池就绪日志：

```text
proxy transport ready: N/M encrypted channels
```

判断直连还是 WSS 见 [transport.zh-CN.md](transport.zh-CN.md)；CDN 可见性见 [security.zh-CN.md](security.zh-CN.md)。

## 相关文件

| 路径 | 用途 |
|---|---|
| `/etc/lynx/cloudflared.yml` | Tunnel ingress |
| `/etc/lynx/cloudflared/<uuid>.json` | Tunnel 凭据 |
| `/etc/lynx/wizard.env` | 向导状态 |
| `/root/.cloudflared/cert.pem` | 账号登录证书 |

卸载 Lynx 会删单元与 `/etc/lynx`，**不会**卸载 `cloudflared` 软件包，也不会自动删除 Cloudflare 侧 Tunnel；不需要时请在控制台清理。
