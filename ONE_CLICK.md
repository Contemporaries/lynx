# 一键部署（Linux）

面向 Lynx **v2.1.0**：本机 SOCKS5/HTTP，订阅走 nginx 443，直连 mTLS 8443，WSS 走 Cloudflare Tunnel。

## 准备

- 已托管在 Cloudflare 的 CDN 子域名（WSS）
- 已有公网证书的订阅域名 + nginx（443）
- 本机可构建或已有 Release 二进制（`./deploy/build.sh`）

## 首次部署服务端

```bash
cd lynx
./deploy/build.sh
sudo ./lynx-wizard.sh
```

按提示填写：

1. CDN 域名（如 `cdn.example.com`）
2. 订阅域名（如 `subscribe.example.com`，默认 443，勿与直连 8443 冲突）
3. 是否启用直连（开放 TCP 8443）
4. 首台设备名

完成后：

- `lynx-server` / `lynx-cloudflared` 以用户 `lynx` 运行
- 打印订阅 URL 与客户端包路径
- 配置 nginx：见 [deploy/nginx-subscribe.conf.example](deploy/nginx-subscribe.conf.example)

请备份 `/etc/lynx/pki/ca.key`。

## 添加设备

```bash
sudo lynx-wizard --add-device
```

客户端包内含单文件 `client.json`（`subscribe_url`）及可选 `lynx-client` 二进制与 `install-client.sh`。

## 安装客户端

```bash
# 解压客户端包后
sudo ./install-client.sh

# 或
lynx-client -subscribe 'https://subscribe.example.com/_lynx/v1/subscribe/<token>' \
  -config /etc/lynx/client.json
```

首次启动会拉取证书并写回同一 `client.json`。

## 验证

```bash
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
sudo lynx-wizard --show-subscribe
curl -sS http://127.0.0.1:8080/_lynx/v1/version
```

## 常用命令

```bash
sudo lynx-wizard --status
sudo lynx-wizard --upgrade-server
sudo lynx-wizard --upgrade-client
sudo ./deploy/uninstall-client.sh
sudo lynx-wizard --uninstall
```

更多：[docs/upgrade.md](docs/upgrade.md)、[docs/uninstall.md](docs/uninstall.md)。
