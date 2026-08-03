# 更新流程

## 版本与发布物

正式版本通过 GitHub tag（如 `v2.1.0`）触发 [Release 工作流](../.github/workflows/release.yml)，产物包括：

| 文件 | 说明 |
|---|---|
| `lynx-server-linux-amd64` / `arm64` | 服务端 |
| `lynx-client-linux-amd64` / `arm64` | Linux 客户端 |
| `lynx-client-windows-amd64.exe` | Windows 客户端 |
| `SHA256SUMS` | 校验和 |

发布页：https://github.com/Contemporaries/lynx/releases

## 发布新版本（维护者）

```bash
go test ./...
./deploy/build.sh

git tag -a v2.1.0 -m "v2.1.0"
git push origin main
git push origin v2.1.0
```

推送 `v*` tag 后，GitHub Actions 自动构建并创建 Release。

## Linux 一键升级（推荐）

```bash
sudo ./deploy/upgrade-server.sh
sudo ./deploy/upgrade-server.sh v2.1.0
sudo lynx-wizard --upgrade-server

sudo ./deploy/upgrade-client.sh
sudo lynx-wizard --upgrade-client
```

## 升级到 2.0+（单文件客户端配置）

- 客户端不再支持 `cert_file` / `key_file` / `ca_file`
- 使用一份 `client.json`：`subscribe_url` 和/或内联 PEM
- 端口约定：订阅走 nginx **443** path；直连 mTLS **8443**

```bash
lynx-client -subscribe 'https://subscribe.example.com/_lynx/v1/subscribe/<token>' -config /etc/lynx/client.json
```

## 升级 Linux 服务端（手动）

```bash
sudo tar -czf ~/lynx-backup-$(date +%Y%m%d).tar.gz /etc/lynx
sudo systemctl stop lynx-server
sudo install -m0755 lynx-server-linux-amd64 /usr/local/bin/lynx-server
sudo systemctl start lynx-server
```

## 升级 Linux 客户端

```bash
sudo ./deploy/upgrade-client.sh
```

## 升级 Windows 客户端

1. 退出旧进程
2. 用新 `lynx-client-windows-amd64.exe` 覆盖
3. 使用 `-subscribe` 或 `subscribe_url` 更新单文件 `client.json`

## 回滚

```bash
sudo systemctl stop lynx-server
sudo tar -xzf ~/lynx-backup-YYYYMMDD.tar.gz -C /
sudo systemctl start lynx-server
```

## 证书与设备

更新程序**不会**自动轮换设备证书。吊销设备请在服务端 `clients` 中设 `"enabled": false`、删除对应条目，或轮换 `subscribe_token`，然后重启 `lynx-server`。
