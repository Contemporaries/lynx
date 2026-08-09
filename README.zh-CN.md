# Lynx

[English](README.md) | **中文**

Lynx **v2.2.2** 是一套加密 TCP 代理：应用走本机 SOCKS5 / HTTP，客户端经 **直连 mTLS** 或 **Cloudflare WebSocket** 到达服务端；内层为 TLS 1.3 + 每设备 mTLS。

不是系统 VPN：不创建 TUN，不改默认路由或系统 DNS。

```text
应用程序
  ├─ SOCKS5 127.0.0.1:1080
  └─ HTTP   127.0.0.1:8080
             │
        Lynx 客户端（单文件 client.json）
             │
    ┌────────┴─────────┐
    │                  │
直连 mTLS :8443    Cloudflare WSS
    │                  │
    └──── Lynx 服务端 ──── Internet
              ▲
         内层 TLS 1.3 + mTLS
```

## 当前能力

- 单文件客户端：`client.json`（`subscribe_url` 和/或内联 PEM）
- 订阅：`https://subscribe.example.com/_lynx/v1/subscribe/<token>`（nginx **443** path）
- 直连：`direct_listen` 默认 **:8443**（mTLS）
- 数据面：Cloudflare WSS + 内层端到端 TLS（CDN 看不到代理明文）
- 本地 SOCKS5（TCP CONNECT + UDP ASSOCIATE）与 HTTP / HTTP CONNECT
- 多路复用连接池；默认禁止代理到私有网段
- 非本机监听时强制本地代理认证
- Linux / Windows CLI；systemd 一键部署

## 非目标

- 系统级 VPN / 全局路由
- 以 QUIC 作为客户端↔服务端传输（Cloudflare Tunnel 到源仍可能用 QUIC）
- 尚未经过独立安全审计

## 端口约定

| 用途 | 默认 |
|---|---|
| 订阅（nginx → origin `127.0.0.1:8080`） | HTTPS **443**（`/_lynx/v1/subscribe/`） |
| 直连 mTLS | TCP **8443** |
| 服务端 WebSocket origin | `127.0.0.1:8080`（Tunnel 仅放行 connect） |
| 客户端 SOCKS5 / HTTP | `127.0.0.1:1080` / `127.0.0.1:8080` |

订阅与直连不可共用同一端口。

## 直连与 WSS（摘要）

| | 直连 mTLS | Cloudflare WSS |
|---|---|---|
| 链路 | 客户端 → 公网 **:8443** → 服务端 | 客户端 → Cloudflare Tunnel → 本机 origin |
| 延迟 / 吞吐 | 通常更好 | 多一跳 CDN，一般略慢 |
| 网络兼容 | 依赖 8443 可达 | 走 443，防火墙下往往更容易 |
| 暴露面 | 需开放 TCP **8443** | 可不对公网开直连口 |
| 元数据 | 直达你的服务器 | CF 可见连接元数据，不能解内层 TLS |

客户端 `mode`：`direct` / `wss` / `auto`（默认先试 WSS，失败再直连；跑在直连时后台探测 WSS——仅运行时切换，不改写 `client.json`）。日志会打印当前链路（`path` / `via`）。完整对比：[docs/transport.zh-CN.md](docs/transport.zh-CN.md)。

## 安装（clone → install）

推荐在 Linux 服务器上（root）。构建需 Go **1.24+**；WSS 需 Cloudflare CDN 域名；订阅需带公网证书的 nginx HTTPS（443）。

```bash
git clone https://github.com/Contemporaries/lynx.git
cd lynx
./deploy/build.sh
sudo ./lynx-wizard.sh
# 等价：sudo ./install.sh
```

向导会安装二进制、PKI、Cloudflare Tunnel、`server.json`、systemd 单元并生成客户端包。随后：

1. 配置 nginx 订阅反代：[deploy/nginx-subscribe.conf.example](deploy/nginx-subscribe.conf.example)
2. 备份 `/etc/lynx/pki/ca.key`
3. 在各设备安装客户端包或执行订阅

```bash
sudo lynx-wizard --show-subscribe
sudo systemctl status lynx-server lynx-cloudflared

lynx-client -subscribe 'https://subscribe.example.com/_lynx/v1/subscribe/<token>' \
  -config /etc/lynx/client.json
```

验证：

```bash
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
curl -x http://127.0.0.1:8080 https://ifconfig.me
```

完整清单：[ONE_CLICK.zh-CN.md](ONE_CLICK.zh-CN.md)。配置 / Cloudflare / 安全：[docs/configuration.zh-CN.md](docs/configuration.zh-CN.md) · [docs/cloudflare.zh-CN.md](docs/cloudflare.zh-CN.md) · [docs/security.zh-CN.md](docs/security.zh-CN.md)。

### 预编译二进制

**https://github.com/Contemporaries/lynx/releases** — linux amd64/arm64 的 server/client、Windows client、`SHA256SUMS`。

Windows：[docs/windows.zh-CN.md](docs/windows.zh-CN.md)。

## 文档

默认英文；下列为中文版。

| 文档 | 内容 |
|---|---|
| [ONE_CLICK.zh-CN.md](ONE_CLICK.zh-CN.md) | clone → build → 向导安装 |
| [docs/configuration.zh-CN.md](docs/configuration.zh-CN.md) | nginx、server/client JSON |
| [docs/cloudflare.zh-CN.md](docs/cloudflare.zh-CN.md) | Tunnel、DNS、Access、WSS |
| [docs/security.zh-CN.md](docs/security.zh-CN.md) | mTLS、token、限流 |
| [docs/transport.zh-CN.md](docs/transport.zh-CN.md) | 直连与 WSS |
| [docs/development.zh-CN.md](docs/development.zh-CN.md) | 开发构建 |
| [docs/windows.zh-CN.md](docs/windows.zh-CN.md) | Windows 客户端 |
| [docs/upgrade.zh-CN.md](docs/upgrade.zh-CN.md) | 升级与发布 |
| [docs/uninstall.zh-CN.md](docs/uninstall.zh-CN.md) | 卸载 |

## 升级 / 卸载

- 升级：[docs/upgrade.zh-CN.md](docs/upgrade.zh-CN.md)
- 卸载：[docs/uninstall.zh-CN.md](docs/uninstall.zh-CN.md)

```bash
sudo ./deploy/upgrade-server.sh
sudo ./deploy/upgrade-client.sh
sudo ./deploy/uninstall-client.sh
sudo lynx-wizard --uninstall
```

## 安全说明（摘要）

- 设备授权：客户端证书 SHA-256 指纹 + `enabled`
- 订阅 token 等同密钥，泄露则轮换或禁用设备
- 速率限制见服务端 `security`
- Cloudflare 可见域名、源 IP、时长与流量体积，无法解密内层 TLS

全文：[docs/security.zh-CN.md](docs/security.zh-CN.md)。

## License

[MIT](LICENSE)
