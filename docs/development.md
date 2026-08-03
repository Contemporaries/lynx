# 开发与构建环境

## 依赖

| 组件 | 版本建议 | 用途 |
|---|---|---|
| Go | 1.24+ | 服务端 / CLI |
| Git | 任意 | 克隆仓库 |
| OpenSSL | 3.x | 生成证书 |

Linux 额外：`systemd`（安装向导）、可选 `cloudflared`（CDN Tunnel）、`nginx`（订阅 HTTPS）。

## 安装 Go（Linux）

```bash
curl -fsSL https://go.dev/dl/go1.24.5.linux-amd64.tar.gz -o /tmp/go.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf /tmp/go.tar.gz
echo 'export PATH=/usr/local/go/bin:$HOME/go/bin:$PATH' >> ~/.bashrc
source ~/.bashrc
go version
```

## 克隆与校验

```bash
git clone https://github.com/Contemporaries/lynx.git
cd lynx
go test ./...
go vet ./...
```

## 构建 CLI（Linux / Windows）

```bash
./deploy/build.sh
ls dist/
```

产物：

- `lynx-server-linux-amd64` / `lynx-server-linux-arm64`
- `lynx-client-linux-amd64` / `lynx-client-linux-arm64`
- `lynx-client-windows-amd64.exe`

## 代理（可选）

若拉取模块较慢：

```bash
export GOPROXY=https://proxy.golang.org,direct
```
