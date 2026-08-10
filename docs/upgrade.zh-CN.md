# 升级与发布

[English](upgrade.md) | **中文**

Lynx **v2.3.0** 升级路径与 Release 发布说明。

## Release 产物

GitHub Releases（`Contemporaries/lynx`）通常包含：

- `lynx-server-linux-amd64` / `arm64`
- `lynx-client-linux-amd64` / `arm64`
- `lynx-client-windows-amd64.exe`
- `SHA256SUMS`

## 发布（维护者）

1. 确认 `main` 与向导/`install.sh` 版本与 tag 一致
2. 打 tag 并推送，例如：

```bash
git tag v2.3.0
git push origin v2.3.0
```

3. CI（`.github/workflows/release.yml`）构建 CLI 并挂到 Release

## 升级服务端

```bash
sudo ./deploy/upgrade-server.sh
sudo ./deploy/upgrade-server.sh v2.3.0
sudo lynx-wizard --upgrade-server [tag]
```

典型行为：下载 Release → 校验 `SHA256SUMS` → 备份到 `/var/backups/lynx/`（及 `/etc/lynx`）→ 替换二进制 → 重启单元。

升级**不会**轮换设备证书。

## 升级客户端

```bash
sudo ./deploy/upgrade-client.sh
sudo lynx-wizard --upgrade-client [tag]
```

## 手工升级

1. 从 Releases 下载对应二进制与 `SHA256SUMS` 并校验
2. 覆盖 `/usr/local/bin/lynx-server` 或 `lynx-client`
3. 按需 `systemctl restart` 相关单元

## 回滚

从 `/var/backups/lynx/` 恢复二进制（及可选 `/etc/lynx`），再重启服务。

## 升级后检查

```bash
lynx-server -version
lynx-client -version
sudo systemctl status lynx-server lynx-cloudflared lynx-client
curl -sS http://127.0.0.1:8080/_lynx/v1/version
```

配置字段：[configuration.zh-CN.md](configuration.zh-CN.md)。
