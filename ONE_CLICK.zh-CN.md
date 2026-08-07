# 一键部署（Linux）

[English](ONE_CLICK.md) | **中文**

面向 Lynx **v2.2.0**：本机 SOCKS5/HTTP；订阅走 nginx **443**；直连 mTLS **8443**；WSS 走 Cloudflare Tunnel。

深入阅读：[docs/configuration.zh-CN.md](docs/configuration.zh-CN.md) · [docs/cloudflare.zh-CN.md](docs/cloudflare.zh-CN.md) · [docs/security.zh-CN.md](docs/security.zh-CN.md)。

## 开始前准备

1. 已托管在 Cloudflare 的 CDN 子域名（如 `cdn.example.com`）用于 WSS
2. 已有公网证书的订阅域名 + nginx（如 `subscribe.example.com`，默认 **443**；**勿**用直连口 **8443**）
3. 若启用直连加速：安全组开放 **TCP 8443**
4. 可在浏览器完成 `cloudflared tunnel login`

## 部署服务端

```bash
./deploy/build.sh
sudo ./lynx-wizard.sh
# 等价：sudo ./install.sh
```

向导典型提问：

1. CDN 域名 — 如 `cdn.example.com`
2. 订阅域名 — 如 `subscribe.example.com`
3. 是否启用直连（开放 TCP 8443）— 默认否
4. 直连域名（若启用）— 如 `direct.example.com`（**DNS only** / 灰云）
5. 第一台设备名 — 默认 `laptop`
6. 可选：CDN 是否已启用 Cloudflare Access Service Token（Client-Id / Secret）

向导会完成：

- `lynx-server` / `lynx-cloudflared` 及 `/etc/lynx` 配置
- PKI（`/etc/lynx/pki`、`/etc/lynx/certs`）— **请备份 `ca.key`**
- Tunnel 仅放行 `/_lynx/v1/connect` → `127.0.0.1:8080`
- 客户端包（含订阅 URL；启用直连时含直连字段）

随后配置 nginx 订阅反代：[deploy/nginx-subscribe.conf.example](deploy/nginx-subscribe.conf.example)。

```bash
sudo lynx-wizard --show-subscribe
sudo systemctl status lynx-server lynx-cloudflared
```

预期输出类似：

```text
Cloudflare 代理入口：wss://cdn.example.com/_lynx/v1/connect
订阅地址：https://subscribe.example.com/_lynx/v1/subscribe/<token>
```

## 添加设备

```bash
sudo lynx-wizard --add-device
```

## 安装客户端（Linux）

使用向导生成的客户端包，或手动：

```bash
sudo install -m 755 lynx-client-linux-amd64 /usr/local/bin/lynx-client
sudo lynx-client -subscribe 'https://subscribe.example.com/_lynx/v1/subscribe/<token>' \
  -config /etc/lynx/client.json
```

## 验证

```bash
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
curl -x http://127.0.0.1:8080 https://ifconfig.me
```

## 常用向导命令

```bash
sudo lynx-wizard --status
sudo lynx-wizard --show-subscribe
sudo lynx-wizard --add-device
sudo lynx-wizard --upgrade-server [tag]
sudo lynx-wizard --upgrade-client [tag]
sudo lynx-wizard --uninstall-client
sudo lynx-wizard --uninstall
```

交互菜单含：首次部署、添加设备、状态、订阅探测、升级、卸载。
