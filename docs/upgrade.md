# 升级与发布

当前版本：**v2.1.0**。

## Release 产物

打 `v*` tag 后由 [Release 工作流](../.github/workflows/release.yml) 构建：

| 文件 | 说明 |
|---|---|
| `lynx-server-linux-amd64` / `arm64` | 服务端 |
| `lynx-client-linux-amd64` / `arm64` | Linux 客户端 |
| `lynx-client-windows-amd64.exe` | Windows 客户端 |
| `SHA256SUMS` | 校验和 |

https://github.com/Contemporaries/lynx/releases

## 维护者发布

```bash
go test ./...
./deploy/build.sh

git tag -a v2.1.0 -m "v2.1.0"
git push origin main
git push origin v2.1.0
```

## Linux 升级（推荐）

```bash
# 服务端
sudo ./deploy/upgrade-server.sh          # latest
sudo ./deploy/upgrade-server.sh v2.1.0
sudo lynx-wizard --upgrade-server

# 客户端
sudo ./deploy/upgrade-client.sh
sudo lynx-wizard --upgrade-client
```

脚本会下载 Release 产物、校验 SHA256、备份旧二进制，并重启对应 systemd 单元。

## 手动升级服务端

```bash
sudo tar -czf ~/lynx-backup-$(date +%Y%m%d).tar.gz /etc/lynx
sudo systemctl stop lynx-server
sudo install -m0755 lynx-server-linux-amd64 /usr/local/bin/lynx-server
sudo systemctl start lynx-server
sudo systemctl status lynx-server --no-pager
lynx-server -version
```

配置对照 [configs/server.json](../configs/server.json)。Cloudflare Tunnel 仅需放行 `path: ^/_lynx/v1/connect$`；订阅走 nginx。

## 手动升级客户端

```bash
sudo systemctl stop lynx-client
sudo install -m0755 lynx-client-linux-amd64 /usr/local/bin/lynx-client
sudo systemctl start lynx-client
```

保留 `/etc/lynx/client.json`（单文件，含内联 PEM 或 `subscribe_url`）。

## Windows

覆盖 `lynx-client-windows-amd64.exe`，必要时用 `-subscribe` 刷新 `client.json`。

## 回滚

```bash
sudo systemctl stop lynx-server
sudo tar -xzf ~/lynx-backup-YYYYMMDD.tar.gz -C /
# 恢复对应版本的 /usr/local/bin/lynx-server
sudo systemctl start lynx-server
```

## 设备与证书

升级不会自动轮换设备证书。吊销：在 `clients` 中设 `enabled: false`、删除条目，或轮换 `subscribe_token`，然后重启 `lynx-server`。
