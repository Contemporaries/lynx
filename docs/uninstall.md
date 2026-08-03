# 卸载

## Linux 仅客户端（推荐）

```bash
sudo ./deploy/uninstall-client.sh
# 客户端安装包内：sudo ./uninstall-client.sh
# 或：sudo lynx-wizard --uninstall-client
```

将执行：

1. 停止并禁用 `lynx-client`
2. 删除 systemd 单元与 `/usr/local/bin/lynx-client`
3. 删除单文件配置 `/etc/lynx/client.json`（含内联 PEM）
4. 清理旧版残留证书目录与 `subscribe-cache`
5. 若同机存在 `/etc/lynx/server.json`，保留服务端配置与 PKI

## Linux 服务端（彻底卸载）

```bash
sudo lynx-wizard --uninstall
# 或：sudo ./lynx-wizard.sh --uninstall
```

会删除 `/etc/lynx`（含私人 CA 与设备证书）。卸载前请备份 `/etc/lynx/pki/ca.key`。不会卸载 `cloudflared` 软件包。

## Windows 客户端

1. 结束 `lynx-client-windows-amd64.exe`
2. 删除程序目录（含可执行文件与 `client.json`）
3. 移除任务计划 / 自启（若有）
4. 取消浏览器或其他软件中的本地代理设置

## 卸载后检查

```bash
systemctl status lynx-server lynx-client lynx-cloudflared 2>&1 | head
ls /etc/lynx 2>&1
which lynx-client lynx-server 2>&1
```
