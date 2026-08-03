# 直连 mTLS 与 Cloudflare WSS

适用 Lynx **v2.1.0**。数据面有两种到达服务端的方式；订阅（nginx 443 path）与二者独立，不替代数据连接。

两端最终都是内层 TLS 1.3 + 每设备 mTLS。CDN **看不到**代理明文，但看得到域名、体积、时长等元数据。

## 路径与形态

| | 直连 mTLS | Cloudflare WSS |
|---|---|---|
| 链路 | 客户端 → 你的公网 IP/域名 **:8443** → 服务端 | 客户端 → Cloudflare → Tunnel → 本机 `127.0.0.1:8080` → 服务端 |
| 外层 | TLS 1.3 + 设备证书（mTLS） | HTTPS/WSS（可加 CF Access）+ **内层**再套 TLS 1.3 + mTLS |
| 典型端口 | **8443** | **443**（走 CDN） |

## 体验对比

| 维度 | 直连 | WSS |
|---|---|---|
| **延迟 / 吞吐** | 通常更好（少一跳 CDN） | 多经 Cloudflare + Tunnel，一般更慢一点 |
| **稳定性** | 依赖服务器公网与防火墙放行 8443 | 依赖 Cloudflare / Tunnel；本机可不暴露 8443 |
| **网络兼容** | 部分网络拦非常规端口或直连 IP | 443 + CDN，穿透防火墙往往更容易 |
| **暴露面** | 需对公网开 **TCP 8443** | 数据面可不对公网开直连口；订阅仍走 nginx 443 |
| **依赖** | 只要直连域名/IP 可达 | 需要 Tunnel、CDN 域名，可选 CF Access |
| **隐私（元数据）** | 流量直接到你的服务器 | Cloudflare 可见连接元数据，**不能**解内层内容 |

## 客户端 `mode`

`client.json` 中：

| `mode` | 行为 |
|---|---|
| `"direct"` | 只用直连 |
| `"wss"` | 只用 WSS |
| `"auto"`（默认） | 先试直连（约 5s），失败再回退 WSS |

一键安装且启用直连时，客户端包通常为 `"mode": "auto"`：能直连就直连，端口不可达时自动走 WSS。

## 怎么选

- **优先速度、服务器 8443 稳通** → 直连（或保持 `auto`）
- **公司网 / 严格防火墙 / 不想暴露 8443** → WSS，或只开 Tunnel、直连绑本机
- **两边都要** → `auto`

## 如何判断当前走的哪条路

客户端成功连上时**不会**固定打印「正在用直连 / WSS」。可这样判断：

1. **看配置**：`mode` 为 `direct` / `wss` 时路径已固定。
2. **`auto` 看日志**：出现  
   `direct TLS unavailable, falling back to Cloudflare WSS`  
   → 该次 dial 走的是 WSS；无此条且已 `proxy transport ready` → 很大概率是直连成功。  
   通道重连时可能再选；多通道时也可能各自不同。
3. **看出站连接**（最可靠）：

```bash
ss -tnp | grep -E 'lynx-client|8443'
```

- 目标为直连域名/IP:**8443** → 直连 mTLS  
- 目标为 CDN 域名:**443** → Cloudflare WSS  
