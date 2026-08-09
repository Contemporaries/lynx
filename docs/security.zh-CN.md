# 安全

[English](security.md) | **中文**

Lynx **v2.2.2** 的威胁模型、信任边界与控制项。

## 是什么 / 不是什么

- 加密 **TCP** 代理，并支持 **SOCKS5 UDP ASSOCIATE** 转发 datagram：应用走本机 SOCKS5 / HTTP CONNECT
- **不是**系统 VPN：无 TUN、不改默认路由或系统 DNS
- 不以 QUIC 作为客户端↔服务端传输（WSS/直连仍基于 TCP）
- 尚未独立安全审计

## 信任边界

```text
[应用] --本机--> [Lynx 客户端] ==加密隧道==> [Lynx 服务端] --TCP--> [互联网目标]
                      |                                    |
                 client.json                         server.json + PKI
```

| 区段 | 保护 |
|---|---|
| 应用 ↔ 客户端 | 默认本机；非 loopback 监听**必须**代理用户名/密码 |
| 客户端 ↔ 服务端（直连） | TLS 1.3 + 双向客户端证书；ALPN |
| 客户端 ↔ 服务端（WSS） | 外层 HTTPS/WSS（可选 CF Access）+ **内层** TLS 1.3 + 客户端证书 |
| 服务端 ↔ 目标 | 按请求转发明文 TCP 或 UDP（受私网策略约束） |

## 设备授权（mTLS）

1. 向导/PKI 为每台设备签发客户端证书（Lynx CA）
2. 服务端 `clients.<name>.certificate_sha256` 必须匹配指纹（小写十六进制、无冒号）
3. `enabled: false` 拒绝该设备会话与订阅
4. 未授权指纹会被拒绝；日志可能含 `rejected unauthorized client fingerprint=…`

直连在外层 TLS 使用设备证书；WSS 在 WebSocket 建立后于**内层** TLS 使用（`ws_inner_server_name` 默认 `lynx.internal`）。

## 订阅 token

```text
https://subscribe.example.com/_lynx/v1/subscribe/<token>
```

- Token 是**密钥**（建议 `openssl rand -hex 32`）
- 未知或禁用 → 不透明 **HTTP 404**
- 响应含 `ca_pem` / `cert_pem` / `key_pem`（完整设备材料）；须 HTTPS；`Cache-Control: no-store`
- 默认限流：每 IP 每分钟 30、每 token 每分钟 10 → HTTP 429
- 限流用源 IP 优先 `CF-Connecting-IP`，其次 `X-Forwarded-For`（nginx 需正确传递）

未配置任何 `subscribe_token` 时订阅恒为 404，服务端打 WARNING。

泄露后：轮换 `subscribe_token` 或 `enabled: false`。

## Cloudflare 可见性

| CF **能**看到 | CF **不能**看到 |
|---|---|
| CDN 域名、客户端公网 IP、时长、字节数 | 内层 TLS 明文 |
| 存在通往 `/_lynx/v1/connect` 的 WSS | SOCKS 目标主机/端口、代理载荷 |
| Access 成败（若启用） | 直连路径流量（不经 CF） |

可选 **Access Service Token** 在边缘拦 WSS；内部仍要设备 mTLS。见 [cloudflare.zh-CN.md](cloudflare.zh-CN.md)。

## 私网保护

默认 `allow_private_networks: false`，禁止代理到 loopback、未指定、组播、链路本地、**RFC1918** 等。TCP dial 或 UDP 目的地址被拒时类似 `target address is not allowed`。仅在确实需要经服务端访问内网时设为 `true`。

## SOCKS5 UDP ASSOCIATE

- SOCKS 监听开启时自动可用。
- Datagram 与 TCP 共用同一加密 mux（直连或 WSS）。
- 不支持 SOCKS UDP 分片（`FRAG != 0` 丢弃）。
- HTTP 代理无 UDP 模式。
- TCP 控制连接断开即结束关联；空闲关联也会超时（`flow_idle_timeout_seconds`）。

## 速率与会话限制

`server.json` 的 `security`（默认）：

| 字段 | 默认 | 作用 |
|---|---|---|
| `max_sessions_per_certificate` | 4 | 每设备证书并发隧道 |
| `max_sessions_per_source_ip` | 8 | 每源 IP 并发隧道 |
| `max_total_sessions` | 256 | 全局 |
| `max_flows_per_certificate` | 512 | 每证书并发流 |
| `max_new_flows_per_second` | 50 | 新建流速率 |
| `handshake_timeout_seconds` | 10 | 握手 |
| `session_idle_timeout_seconds` | 300 | 会话空闲 |
| `flow_idle_timeout_seconds` | 600 | 流空闲 |
| `max_subscribe_per_ip_per_min` | 30 | 订阅 / IP |
| `max_subscribe_per_token_per_min` | 10 | 订阅 / token |

另有 `max_proxy_flows_per_session`（256）、`proxy_dial_timeout_seconds`（15）。

## 本地代理认证

- `socks_listen` / `http_listen` 优先 `127.0.0.1`
- 绑定非本机地址时**必须**设置 `proxy_username` / `proxy_password`

## 磁盘上的密钥材料

| 路径 | 敏感度 |
|---|---|
| `/etc/lynx/pki/ca.key` | **极高** — 请离线备份 |
| 设备 `*.key`、`client.json` 内 PEM | 设备身份 |
| `server.json` 中的 subscribe token | 可拉取设备 PEM |
| `/etc/lynx/cloudflared/*.json` | Tunnel 凭据 |

升级**不会**轮换设备证书。全量卸载会删 `/etc/lynx` — 先备份。

## 运维清单

1. 订阅只走 HTTPS（nginx 真证书）
2. 勿分享订阅 URL 或含 PEM 的 `client.json`
3. 直连域名用灰云；按需开放 TCP 8443
4. CDN 暴露面大时建议对 WSS 开 CF Access
5. 保持 `allow_private_networks: false`，除非必要
6. 丢失设备：禁用或轮换 token，更新 `clients`

## 相关

- [configuration.zh-CN.md](configuration.zh-CN.md)  
- [cloudflare.zh-CN.md](cloudflare.zh-CN.md)  
- [transport.zh-CN.md](transport.zh-CN.md)  
