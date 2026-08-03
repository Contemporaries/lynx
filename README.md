# Lynx

Lynx 是一套**加密 TCP 代理**：客户端在本机暴露 SOCKS5 / HTTP 代理端口，经直连 TLS 或 Cloudflare WebSocket 连接到服务端，内层使用 TLS 1.3 与每设备 mTLS。

它**不是**系统级 VPN：不创建 TUN、不修改默认路由或系统 DNS。

```text
应用程序
  ├─ SOCKS5 127.0.0.1:1080
  └─ HTTP   127.0.0.1:8080
             │
        Lynx 客户端连接池
             │
    ┌────────┴─────────┐
    │                  │
直连 TLS           Cloudflare WSS
    │                  │
    └──── Lynx 服务端 ──── Internet
              ▲
         内层 TLS 1.3 + mTLS
```

与 [sing-box](https://github.com/SagerNet/sing-box)、[TUIC](https://github.com/tuic-protocol/tuic)、[Trojan](https://github.com/trojan-gfw/trojan)、[Hysteria](https://github.com/apernet/hysteria) 等协议同属代理生态；Lynx 的差异点是 **Cloudflare WSS 可达性 + 内层端到端 mTLS**（CDN 边缘无法读取代理明文）。

## Features

- TLS 1.3 与每设备独立客户端证书（SHA-256 指纹授权）
- **单文件客户端配置**：一份 `client.json` 或一个订阅 URL；证书以内联 PEM 写入配置
- **订阅**：`https://subscribe.example.com/_lynx/v1/subscribe/<token>`（nginx 443 path）；直连 mTLS 默认 **8443**
- Cloudflare WSS 通道内第二层端到端 TLS（数据面）
- 本地 SOCKS5（TCP CONNECT）与 HTTP / HTTP CONNECT
- 多路复用连接池
- 默认禁止代理到环回、链路本地与私有网络
- 非本机监听时强制本地代理认证
- Linux / Windows CLI

## Non-goals

- 不是系统 VPN，不接管全局路由
- 当前不支持 UDP / QUIC / SOCKS5 UDP ASSOCIATE
- 不做客户端多文件证书路径（`cert_file` 已移除）
- 尚未经过独立安全审计

## 与同类协议对比

| 协议 | 典型传输 | 普通 Cloudflare CDN | UDP | 弱网 | 认证 | 备注 |
|---|---|---:|---:|---:|---|---|
| **Lynx** | TCP + WSS + 内层 TLS | 很好 | 无 | 一般 | mTLS 设备证书指纹 | CDN 看不到内层明文 |
| **Hysteria 2** | QUIC | 不支持普通 HTTP CDN | 原生 | 很好 | TLS + 应用认证 | 弱网优势大 |
| **TUIC v5** | QUIC | 不支持普通 HTTP CDN | 有 | 很好 | TLS Exporter | 多流 / 连接迁移 |
| **Trojan** | TLS/TCP | 原版不直接适配 | 有 | 一般 | 密码摘要 | 简单成熟 |
| **sing-box / VLESS+WS** | 可组合 | 很好 | 依配置 | 一般 | UUID 等 | 生态与分流能力强 |

## 下载

预构建包由 GitHub Actions 在打 tag 后自动发布：

**https://github.com/Contemporaries/lynx/releases**

| 产物 | 说明 |
|---|---|
| `lynx-server-linux-amd64` / `arm64` | 服务端 |
| `lynx-client-linux-amd64` / `arm64` | Linux 客户端 |
| `lynx-client-windows-amd64.exe` | Windows 客户端 |
| `SHA256SUMS` | 校验和 |

## 文档索引

| 文档 | 内容 |
|---|---|
| [docs/development.md](docs/development.md) | Go 环境与本地构建 |
| [ONE_CLICK.md](ONE_CLICK.md) | Linux 一键部署 |
| [docs/windows.md](docs/windows.md) | Windows 客户端 |
| [docs/upgrade.md](docs/upgrade.md) | 更新 / 发布流程 |
| [docs/uninstall.md](docs/uninstall.md) | 卸载 |

## 快速开始

### 环境

见 [docs/development.md](docs/development.md)（Go 1.24+）。

### 本地构建 CLI

```bash
go test ./...
go vet ./...
./deploy/build.sh
```

### 生成证书（CA 私钥离线）

```bash
./deploy/generate-certs.sh direct.example.com lynx.internal laptop ./certs
```

服务端只部署 `ca.crt`、`server.crt`、`server.key`；CA 私钥请离线保存。将 `client-auth.snippet.json` 合并进服务端 `clients`。

### 服务端配置摘录

```json
{
  "direct_listen": ":8443",
  "public_base_url": "https://subscribe.example.com",
  "cdn_base_url": "https://cdn.example.com",
  "ws_path": "/_lynx/v1/connect",
  "subscribe_path_prefix": "/_lynx/v1/subscribe/",
  "clients": {
    "phone": {
      "certificate_sha256": "abcdef...",
      "enabled": true,
      "subscribe_token": "<32字节随机hex>",
      "cert_file": "/etc/lynx/pki/phone.crt",
      "key_file": "/etc/lynx/pki/phone.key"
    }
  }
}
```

`public_base_url` 是 nginx 订阅入口（推荐 **443** path）；`direct_listen` 为 mTLS 直连（推荐 **:8443**）；`cdn_base_url` 用于拼 profile 里的 `ws_url`。nginx 片段见 [deploy/nginx-subscribe.conf.example](deploy/nginx-subscribe.conf.example)。

完整样例：[configs/server.json](configs/server.json)、[configs/client.json](configs/client.json)。

### Linux 一键部署

见 [ONE_CLICK.md](ONE_CLICK.md) 与 `./lynx-wizard.sh`（systemd 以 `User=lynx` 运行）。向导添加设备后会打印：

```text
订阅地址：https://subscribe.example.com/_lynx/v1/subscribe/<token>
```

### 客户端（一份 JSON）

```bash
lynx-client -subscribe 'https://subscribe.example.com/_lynx/v1/subscribe/<token>' \
  -config /etc/lynx/client.json

lynx-client -config /etc/lynx/client.json
```

最小 `client.json`：

```json
{ "subscribe_url": "https://subscribe.example.com/_lynx/v1/subscribe/<token>" }
```

```bash
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
curl -x http://127.0.0.1:8080 https://ifconfig.me
```

## 卸载

完整步骤见 **[docs/uninstall.md](docs/uninstall.md)**。

```bash
sudo ./deploy/uninstall-client.sh
sudo lynx-wizard --uninstall
```

## 更新

完整升级 / 发布 / 回滚流程见 **[docs/upgrade.md](docs/upgrade.md)**。

```bash
git tag -a v2.1.0 -m "v2.1.0"
git push origin v2.1.0
```

将触发 [.github/workflows/release.yml](.github/workflows/release.yml)，构建 Linux / Windows 并创建 GitHub Release。

```bash
sudo ./deploy/upgrade-server.sh v2.1.0
sudo ./deploy/upgrade-client.sh v2.1.0
```

## 安全说明

- 授权绑定客户端证书 **SHA-256 指纹** + `enabled`
- 订阅 URL 中的 token 等同密钥，泄露请在服务端轮换 `subscribe_token` 或 `enabled: false`
- 订阅经 nginx 暴露，勿公开日志中的完整 URL
- 会话 / 源 IP / 流 / 订阅拉取速率限制见服务端 `security` 配置
- Cloudflare 仍可见域名、来源 IP、连接时间与流量大小，但无法解密内层 TLS
- Cloudflare 套餐与条款以 Cloudflare 当前说明为准

## Roadmap

- 并行建连与公平调度
- QUIC 主通道与 UDP
- 证书自动轮换与 CRL
- 第三方审计

## License

[MIT](LICENSE)
