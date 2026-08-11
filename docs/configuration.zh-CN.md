# 配置

[English](configuration.md) | **中文**

Lynx **v2.3.0** 分步配置：端口、nginx 订阅、server/client JSON、验证。Tunnel/Access 详见 [cloudflare.zh-CN.md](cloudflare.zh-CN.md)；威胁模型与控制项见 [security.zh-CN.md](security.zh-CN.md)。

## 架构（端口）

| 角色 | 默认 | 说明 |
|---|---|---|
| 订阅（公网） | HTTPS **443** path `/_lynx/v1/subscribe/` | nginx → `127.0.0.1:8080` |
| 直连 mTLS | TCP **8443** | **不可**与订阅共用端口 |
| 服务端 WS + 订阅 origin | `127.0.0.1:8080` | Tunnel 仅放行 connect |
| 客户端 SOCKS5 | `127.0.0.1:1080` | TCP CONNECT + UDP ASSOCIATE |
| 客户端 HTTP 代理 | `127.0.0.1:8080` | 客户端本机 |

```text
应用 → SOCKS5/HTTP（本机）
         → Lynx 客户端
              ├─ 直连 TLS → direct.example.com:8443
              └─ WSS → cdn.example.com/_lynx/v1/connect → Tunnel → :8080
         Lynx 服务端同时在 :8080 提供订阅（经 nginx 443）
```

## 推荐：clone → install

```bash
git clone https://github.com/Contemporaries/lynx.git
cd lynx
./deploy/build.sh
sudo ./lynx-wizard.sh
```

清单见 [ONE_CLICK.zh-CN.md](../ONE_CLICK.zh-CN.md)。Cloudflare：[cloudflare.zh-CN.md](cloudflare.zh-CN.md)。

完成后在订阅主机配置 **nginx**（下文），再在各设备安装客户端包。

## 步骤 A — nginx 订阅（公网订阅必需）

1. 将 `subscribe.example.com` 指向本机（或能访问 Lynx 机 `127.0.0.1:8080` 的主机，通常同机）
2. 为该域名申请公网 TLS 证书
3. 在 catch-all `/` **之前**加入 location（样例：[deploy/nginx-subscribe.conf.example](../deploy/nginx-subscribe.conf.example)）：

```nginx
location ^~ /_lynx/v1/subscribe {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header CF-Connecting-IP $remote_addr;
    proxy_connect_timeout 10s;
    proxy_send_timeout 30s;
    proxy_read_timeout 30s;
    proxy_buffering off;
    add_header Cache-Control "no-store" always;
}
```

4. 重载 nginx 并自检：

```bash
curl -sS http://127.0.0.1:8080/_lynx/v1/version
sudo lynx-wizard --show-subscribe
curl -sS "https://subscribe.example.com/_lynx/v1/subscribe/<token>" | head
```

`server.json` 的 `public_base_url` 须为客户端使用的 HTTPS 源，如 `https://subscribe.example.com`（不要带 `:8443`）。

## 步骤 B — server.json

运行时路径：`/etc/lynx/server.json`。样例：[configs/server.json](../configs/server.json)。

### 核心字段

| 字段 | 含义 |
|---|---|
| `direct_listen` | 直连 mTLS。公网 `:8443`；禁用直连时用 `127.0.0.1:8443` |
| `ws_listen` | WS + 订阅 origin（默认 `127.0.0.1:8080`） |
| `ws_path` | 默认 `/_lynx/v1/connect` |
| `public_base_url` | 对外订阅 base |
| `cdn_base_url` | 用于拼客户端 `ws_url` |
| `subscribe_path_prefix` | 默认 `/_lynx/v1/subscribe/` |
| `cert_file` / `key_file` | 服务端 TLS |
| `client_ca_file` | 签发设备证书的 CA |
| `clients.<name>` | 每设备授权与订阅材料 |
| `allow_private_networks` | 默认 `false` |
| `security` | 限流 — 见 [security.zh-CN.md](security.zh-CN.md) |
| `log.level` | `debug`/`info`/`warn`/`error` — 见 [logging.zh-CN.md](logging.zh-CN.md) |
| `mgmt` | 可选管理 API — 见 [mgmt-api.zh-CN.md](mgmt-api.zh-CN.md) / [webui.zh-CN.md](webui.zh-CN.md) |

### 设备条目

```json
"laptop": {
  "certificate_sha256": "<客户端证书 sha256>",
  "enabled": true,
  "subscribe_token": "<随机 hex>",
  "cert_file": "/etc/lynx/pki/laptop.crt",
  "key_file": "/etc/lynx/pki/laptop.key"
}
```

```bash
sudo lynx-wizard --add-device
sudo systemctl restart lynx-server   # 手工改配置后
```

## 步骤 C — Cloudflare Tunnel

默认 `/etc/lynx/cloudflared.yml` 仅代理：

```text
cdn.example.com + path ^/_lynx/v1/connect$  →  http://127.0.0.1:8080
```

```bash
sudo systemctl enable --now lynx-cloudflared
sudo systemctl status lynx-cloudflared
```

DNS、Access 等：[cloudflare.zh-CN.md](cloudflare.zh-CN.md)。

## 步骤 D — 直连 mTLS（可选）

1. 防火墙开放 **TCP 8443**
2. `direct.example.com`：**DNS only**（灰云）指向服务器
3. `direct_listen`: `:8443`
4. 客户端含 `direct_addr` / `direct_server_name`，通常 `mode: "auto"`

## 步骤 E — client.json

订阅前最小配置：

```json
{
  "subscribe_url": "https://subscribe.example.com/_lynx/v1/subscribe/<token>"
}
```

或使用向导已含 PEM 与端点的客户端包。

### 重要字段

| 字段 | 默认 | 含义 |
|---|---|---|
| `subscribe_url` | — | 拉取 profile |
| `mode` | `auto` | `direct` / `wss` / `auto`（auto：先 WSS 再直连；直连时探测 WSS；不改写 JSON） |
| `direct_addr` | — | 如 `direct.example.com:8443` |
| `direct_server_name` | — | 直连 SNI |
| `ws_url` | 订阅写入 | 如 `wss://cdn.example.com/_lynx/v1/connect` |
| `ws_inner_server_name` | `lynx.internal` | 内层 mTLS SNI |
| `certificate` / `key` / `certificate_authority` | 订阅写入 | 内联 PEM |
| `cf_access_client_id` / `cf_access_client_secret` | 可选 | Access Service Token |
| `socks_listen` | `127.0.0.1:1080` | 空则关闭；支持 CONNECT + UDP ASSOCIATE |
| `http_listen` | `127.0.0.1:8080` | 仅 TCP（无 UDP） |
| `proxy_channels` | 3（≤8） | 连接池 |
| `proxy_username` / `proxy_password` | — | 非本机监听时必填 |
| `ping_interval_seconds` | 20 | |
| `pong_timeout_misses` | 3 | |
| `subscribe_refresh_seconds` | 0=关 | 定期重订阅 |

模式说明：[transport.zh-CN.md](transport.zh-CN.md)。

### 运行与验证

```bash
lynx-client -subscribe 'https://subscribe.example.com/_lynx/v1/subscribe/<token>' \
  -config /etc/lynx/client.json
lynx-client -config /etc/lynx/client.json

curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
curl -x http://127.0.0.1:8080 https://ifconfig.me
```

Windows：[windows.zh-CN.md](windows.zh-CN.md)。

## 手工部署（无向导）

1. `./deploy/build.sh`
2. 安装 `lynx-server`，准备 `/etc/lynx` 与 PKI
3. 写 `server.json` 并启动
4. 按 [cloudflare.zh-CN.md](cloudflare.zh-CN.md) 配置 Tunnel
5. 配置 nginx（步骤 A）
6. 分发客户端配置或订阅 URL

## 相关

| 文档 | 主题 |
|---|---|
| [ONE_CLICK.zh-CN.md](../ONE_CLICK.zh-CN.md) | 向导清单 |
| [cloudflare.zh-CN.md](cloudflare.zh-CN.md) | Tunnel / DNS / Access |
| [security.zh-CN.md](security.zh-CN.md) | 安全模型 |
| [transport.zh-CN.md](transport.zh-CN.md) | 直连 vs WSS |
| [upgrade.zh-CN.md](upgrade.zh-CN.md) / [uninstall.zh-CN.md](uninstall.zh-CN.md) | 生命周期 |
