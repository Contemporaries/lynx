# Lynx 一键代理部署

面向“其他软件接入本地代理端口”的部署方式：

- 客户端**一份 `client.json`**（或订阅 URL）；证书以内联 PEM 存入该文件
- 不接管默认路由，不修改系统 DNS
- 服务端作为 TCP 代理出口
- Cloudflare CDN：仅 WebSocket 数据面
- 订阅走现有 nginx **443** path；直连 mTLS 独占 **8443**
- systemd 以专用用户 `lynx` 运行

预构建二进制见：https://github.com/Contemporaries/lynx/releases

## 一、首次部署服务端

```bash
cd lynx
./deploy/build.sh
sudo ./lynx-wizard.sh
```

询问 CDN 域名与订阅域名（例如 `cdn.example.com` / `subscribe.example.com`，勿与直连 `8443` 冲突）。

nginx 片段见 [deploy/nginx-subscribe.conf.example](deploy/nginx-subscribe.conf.example)。

请备份 `/etc/lynx/pki/ca.key`。

## 二、添加设备

```bash
sudo lynx-wizard --add-device
```

得到客户端包：仅含 **`client.json`（subscribe_url）** + 可选二进制。

## 三、客户端（Linux / Windows）

```bash
lynx-client -subscribe 'https://subscribe.example.com/_lynx/v1/subscribe/<token>' -config /etc/lynx/client.json
# 或包内
sudo ./install-client.sh
```

首次启动将证书写入同一 `client.json` 的 `certificate` / `key` / `certificate_authority` 字段。

## 四、验证

```bash
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
```

## 五、卸载 / 更新

- [docs/uninstall.md](docs/uninstall.md)
- [docs/upgrade.md](docs/upgrade.md)

```bash
sudo ./deploy/uninstall-client.sh
sudo ./deploy/upgrade-server.sh
sudo ./deploy/upgrade-client.sh
```

**Breaking（2.0+）：** 旧版依赖 `cert_file`/`key_file`/`ca_file` 的客户端配置不再可用，请改用订阅或内联 PEM 的单文件 JSON。
