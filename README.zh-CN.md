# Lynx

[English](README.md) | **中文**

Lynx **v2.1.0** 是一套加密 TCP 代理：应用走本机 SOCKS5 / HTTP，客户端经 **直连 mTLS** 或 **Cloudflare WebSocket** 到达服务端；内层为 TLS 1.3 + 每设备 mTLS。

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
- 本地 SOCKS5（TCP CONNECT）与 HTTP / HTTP CONNECT
- 多路复用连接池；默认禁止代理到私有网段
- 非本机监听时强制本地代理认证
- Linux / Windows CLI；systemd 一键部署

## 非目标

- 系统级 VPN / 全局路由
- UDP / QUIC / SOCKS5 UDP ASSOCIATE
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

客户端 `mode`：`direct` / `wss` / `auto`（默认先试直连，失败回退 WSS）。完整对比：[docs/transport.zh-CN.md](docs/transport.zh-CN.md)。

## 下载

**https://github.com/Contemporaries/lynx/releases**

| 产物 | 说明 |
|---|---|
| `lynx-server-linux-amd64` / `arm64` | 服务端 |
| `lynx-client-linux-amd64` / `arm64` | Linux 客户端 |
| `lynx-client-windows-amd64.exe` | Windows 客户端 |
| `SHA256SUMS` | 校验和 |

## 文档

默认英文；下列为中文版（英文见去掉 `.zh-CN` 的同名文件）。

| 文档 | 内容 |
|---|---|
| [ONE_CLICK.zh-CN.md](ONE_CLICK.zh-CN.md) | Linux 一键部署 |
| [docs/configuration.zh-CN.md](docs/configuration.zh-CN.md) | 配置步骤、nginx、server/client JSON |
| [docs/cloudflare.zh-CN.md](docs/cloudflare.zh-CN.md) | Tunnel、DNS、Access、WSS 连接 |
| [docs/security.zh-CN.md](docs/security.zh-CN.md) | mTLS、token、限流、CDN 可见性 |
| [docs/transport.zh-CN.md](docs/transport.zh-CN.md) | 直连与 WSS |
| [docs/development.zh-CN.md](docs/development.zh-CN.md) | 开发与构建 |
| [docs/windows.zh-CN.md](docs/windows.zh-CN.md) | Windows 客户端 |
| [docs/upgrade.zh-CN.md](docs/upgrade.zh-CN.md) | 升级与发布 |
| [docs/uninstall.zh-CN.md](docs/uninstall.zh-CN.md) | 卸载 |

## 快速开始

### 构建

```bash
# Go 1.24+
go test ./...
./deploy/build.sh
```

详见 [docs/development.zh-CN.md](docs/development.zh-CN.md)。

### 服务端（推荐向导）

```bash
./deploy/build.sh
sudo ./lynx-wizard.sh
```

向导会配置 Cloudflare Tunnel、PKI、`server.json`，并生成客户端包。订阅 URL：

```text
https://subscribe.example.com/_lynx/v1/subscribe/<token>
```

nginx 片段：[deploy/nginx-subscribe.conf.example](deploy/nginx-subscribe.conf.example)。请备份 `/etc/lynx/pki/ca.key`。

详细步骤：[docs/configuration.zh-CN.md](docs/configuration.zh-CN.md) · Cloudflare：[docs/cloudflare.zh-CN.md](docs/cloudflare.zh-CN.md)。

### 服务端配置要点

```json
{
  "direct_listen": ":8443",
  "public_base_url": "https://subscribe.example.com",
  "cdn_base_url": "https://cdn.example.com",
  "ws_path": "/_lynx/v1/connect",
  "subscribe_path_prefix": "/_lynx/v1/subscribe/",
  "clients": {
    "laptop": {
      "certificate_sha256": "<fingerprint>",
      "enabled": true,
      "subscribe_token": "<token>",
      "cert_file": "/etc/lynx/pki/laptop.crt",
      "key_file": "/etc/lynx/pki/laptop.key"
    }
  }
}
```

完整样例：[configs/server.json](configs/server.json)、[configs/client.json](configs/client.json)。

### 客户端

```bash
lynx-client -subscribe 'https://subscribe.example.com/_lynx/v1/subscribe/<token>' \
  -config /etc/lynx/client.json

lynx-client -config /etc/lynx/client.json
```

最小配置：

```json
{
  "subscribe_url": "https://subscribe.example.com/_lynx/v1/subscribe/<token>"
}
```

订阅成功后同一文件写入内联 PEM。验证：

```bash
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
curl -x http://127.0.0.1:8080 https://ifconfig.me
```

Windows 见 [docs/windows.zh-CN.md](docs/windows.zh-CN.md)。

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
