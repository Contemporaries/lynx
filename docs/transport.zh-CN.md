# 直连 mTLS 与 Cloudflare WSS

[English](transport.md) | **中文**

适用 Lynx **v2.2.2**。数据面有两种到达服务端的方式；订阅（nginx 443 path）与二者独立。

两端最终都是内层 TLS 1.3 + 每设备 mTLS。CDN **看不到**代理明文，但看得到域名、体积、时长等元数据。

## 路径与形态

| | 直连 mTLS | Cloudflare WSS |
|---|---|---|
| 链路 | 客户端 → 公网 IP/域名 **:8443** → 服务端 | 客户端 → Cloudflare → Tunnel → `127.0.0.1:8080` → 服务端 |
| 外层 | TLS 1.3 + 设备证书（mTLS） | HTTPS/WSS（可加 CF Access）+ **内层**再套 TLS 1.3 + mTLS |
| 典型端口 | **8443** | **443**（走 CDN） |

## 体验对比

| 维度 | 直连 | WSS |
|---|---|---|
| **延迟 / 吞吐** | 通常更好（少一跳 CDN） | 多经 Cloudflare + Tunnel，一般更慢一点 |
| **稳定性** | 依赖公网与防火墙放行 8443 | 依赖 Cloudflare / Tunnel；可不暴露 8443 |
| **网络兼容** | 部分网络拦非常规端口或直连 IP | 443 + CDN，穿透往往更容易 |
| **暴露面** | 需对公网开 **TCP 8443** | 数据面可不对公网开直连口；订阅仍走 nginx 443 |
| **依赖** | 直连域名/IP 可达 | Tunnel、CDN 域名，可选 CF Access |
| **隐私（元数据）** | 流量直达你的服务器 | Cloudflare 可见连接元数据，**不能**解内层内容 |

## 客户端 `mode`

| `mode` | 行为 |
|---|---|
| `"direct"` | 只用直连 |
| `"wss"` | 只用 WSS |
| `"auto"`（默认） | 先试 WSS（约 20s），失败再直连（约 5s）。跑在直连时约每 15s 探测 WSS，恢复后切回。**不会**改写 `client.json`。 |

一键安装且启用直连时，客户端包通常以 `"mode": "auto"` 起步。

## 怎么选

- 优先走 Cloudflare / 严格防火墙 → 保持 `auto` 或设为 `wss`
- 8443 稳定且要最低延迟 → 设为 `direct`
- 要 WSS 优先并自动跌落/恢复 → `auto`（仅运行时）

Tunnel 与 Access：[cloudflare.zh-CN.md](cloudflare.zh-CN.md)。安全含义：[security.zh-CN.md](security.zh-CN.md)。

## 如何判断当前走的哪条路

1. **看配置**：`mode` 为 `direct` / `wss` 时路径已固定。`auto` 时文件里仍是 `"mode": "auto"`，实际链路只在运行时切换。
2. **看日志**（关注 path / via）：

```text
transport: path=wss kind=cloudflare-wss via=wss://cdn.example.com/_lynx/v1/connect inner_sni=lynx.internal
auto: path changed wss → direct (WSS connection lost: …) via=direct.example.com:8443
transport: path=direct kind=direct-tls via=direct.example.com:8443 sni=direct.example.com
```

3. **看出站连接**：

```bash
ss -tnp | grep -E 'lynx-client|8443'
```

- 目标为直连域名/IP:**8443** → 直连 mTLS  
- 目标为 CDN 域名:**443** → Cloudflare WSS  
