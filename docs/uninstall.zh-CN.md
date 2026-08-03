# 卸载

[English](uninstall.md) | **中文**

移除 Lynx **v2.1.0** 仅客户端或全量服务端。

## 仅客户端

```bash
sudo ./deploy/uninstall-client.sh
sudo lynx-wizard --uninstall-client
```

停止并移除 `lynx-client` 单元/二进制及通常的 `client.json`。**不会**卸载 `cloudflared`、`lynx-server` 或 Tunnel。

## 全量服务端卸载

**请先备份 `/etc/lynx/pki/ca.key`（及仍需的设备材料）。**

```bash
sudo lynx-wizard --uninstall
```

移除 `lynx-server` / `lynx-client` / `lynx-cloudflared` 单元、Lynx 二进制、`/etc/lynx`、`/usr/local/lib/lynx` 等。**不会**卸载 `cloudflared` 软件包本身；不需要时请在 Cloudflare 控制台删除 Tunnel/DNS。

### 手工提纲

```bash
sudo systemctl disable --now lynx-server lynx-client lynx-cloudflared 2>/dev/null || true
sudo rm -f /etc/systemd/system/lynx-*.service
sudo systemctl daemon-reload
sudo rm -f /usr/local/bin/lynx-server /usr/local/bin/lynx-client /usr/local/sbin/lynx-wizard
sudo rm -rf /etc/lynx /usr/local/lib/lynx
```

若曾添加 nginx `/_lynx/v1/subscribe` location，请一并删除。

## Windows 客户端

删除 exe、`client.json` 及自建快捷方式/计划任务即可。

## 检查

```bash
systemctl status lynx-server lynx-client lynx-cloudflared 2>&1 | head
which lynx-server lynx-client 2>/dev/null
```
