# 卸载

## Linux 仅客户端

```bash
sudo ./deploy/uninstall-client.sh
# 客户端包内：sudo ./uninstall-client.sh
# 或：sudo lynx-wizard --uninstall-client
```

效果：

1. 停止并禁用 `lynx-client`
2. 删除单元与 `/usr/local/bin/lynx-client`
3. 删除 `/etc/lynx/client.json`（单文件配置，含内联 PEM）
4. 若存在 `/etc/lynx/server.json`，保留服务端与 PKI，只删客户端相关文件

不会卸载 `cloudflared`，也不会删除 `lynx-server`。

### 手动

```bash
sudo systemctl disable --now lynx-client 2>/dev/null || true
sudo rm -f /etc/systemd/system/lynx-client.service /usr/local/bin/lynx-client
# 纯客户端：
sudo rm -rf /etc/lynx
# 同机有服务端时改为：sudo rm -f /etc/lynx/client.json
sudo systemctl daemon-reload
```

## Linux 服务端（含本机全部组件）

```bash
sudo lynx-wizard --uninstall
# 或：sudo ./lynx-wizard.sh --uninstall
```

停止 `lynx-server` / `lynx-client` / `lynx-cloudflared`，删除二进制、单元与 `/etc/lynx`（含 CA 与设备证书）。卸载前请备份 `/etc/lynx/pki/ca.key`。`cloudflared` 软件包本身不删除。

### 手动

```bash
sudo systemctl disable --now lynx-server lynx-client lynx-cloudflared 2>/dev/null || true
sudo rm -f /etc/systemd/system/lynx-*.service
sudo rm -f /usr/local/bin/lynx-client /usr/local/bin/lynx-server /usr/local/sbin/lynx-wizard
sudo rm -rf /etc/lynx /usr/local/lib/lynx
sudo systemctl daemon-reload
```

## Windows

1. 结束 `lynx-client-windows-amd64.exe`
2. 删除程序目录（可执行文件与 `client.json`）
3. 清除任务计划 / 自启（若有）
4. 取消指向 `127.0.0.1:1080` / `8080` 的代理设置

## 检查

```bash
systemctl status lynx-server lynx-client lynx-cloudflared 2>&1 | head
ls /etc/lynx 2>&1
which lynx-client lynx-server 2>&1
```
