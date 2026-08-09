# 安装：clone → build → 向导

[English](ONE_CLICK.md) | **中文**

Lynx **v2.2.2**（Linux）：本机 SOCKS5/HTTP；订阅走 nginx **443**；可选直连 mTLS **8443**；WSS 走 Cloudflare Tunnel。

## 1. 准备

1. 构建机安装 Go **1.24+**（`go version`）
2. 已在 Cloudflare 托管的 CDN 域名（如 `cdn.example.com`）用于 WSS
3. 已有公网证书的订阅域名 + nginx（如 `subscribe.example.com`，**443**；勿用 **8443**）
4. 若启用直连：安全组开放 **TCP 8443**
5. 可在浏览器完成 `cloudflared tunnel login`

## 2. Clone 并构建

```bash
git clone https://github.com/Contemporaries/lynx.git
cd lynx
./deploy/build.sh
```

## 3. 安装（向导）

```bash
sudo ./lynx-wizard.sh
# 等价：sudo ./install.sh
```

典型提问：

1. CDN 域名 — 如 `cdn.example.com`
2. 订阅域名 — 如 `subscribe.example.com`
3. 是否启用直连（TCP 8443）— 默认否
4. 直连域名（若启用）— 如 `direct.example.com`（**DNS only** / 灰云）
5. 第一台设备名 — 默认 `laptop`
6. 可选 Cloudflare Access Service Token

会安装：

- 二进制与 `lynx-server` / `lynx-cloudflared` 单元
- PKI（`/etc/lynx/pki`、`/etc/lynx/certs`）— **请备份 `ca.key`**
- Tunnel：仅 `/_lynx/v1/connect` → `127.0.0.1:8080`
- 客户端包（订阅 URL；启用直连时含直连字段）

## 4. nginx 订阅

按 [deploy/nginx-subscribe.conf.example](deploy/nginx-subscribe.conf.example) 加入 location 并重载 nginx。

```bash
sudo lynx-wizard --show-subscribe
sudo systemctl status lynx-server lynx-cloudflared
curl -sS http://127.0.0.1:8080/_lynx/v1/version
```

预期：

```text
Cloudflare 代理入口：wss://cdn.example.com/_lynx/v1/connect
订阅地址：https://subscribe.example.com/_lynx/v1/subscribe/<token>
```

## 5. 客户端

使用向导生成的包，或：

```bash
sudo install -m 755 dist/lynx-client-linux-amd64 /usr/local/bin/lynx-client
sudo lynx-client -subscribe 'https://subscribe.example.com/_lynx/v1/subscribe/<token>' \
  -config /etc/lynx/client.json
```

更多设备：

```bash
sudo lynx-wizard --add-device
```

## 6. 验证

```bash
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
curl -x http://127.0.0.1:8080 https://ifconfig.me
```

## 常用命令

```bash
sudo lynx-wizard --status
sudo lynx-wizard --show-subscribe
sudo lynx-wizard --add-device
sudo lynx-wizard --upgrade-server [tag]
sudo lynx-wizard --upgrade-client [tag]
sudo lynx-wizard --uninstall-client
sudo lynx-wizard --uninstall
```

深入阅读：[docs/configuration.zh-CN.md](docs/configuration.zh-CN.md) · [docs/cloudflare.zh-CN.md](docs/cloudflare.zh-CN.md) · [docs/security.zh-CN.md](docs/security.zh-CN.md) · [docs/transport.zh-CN.md](docs/transport.zh-CN.md)。
