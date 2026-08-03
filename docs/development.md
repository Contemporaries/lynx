# 开发与构建

当前版本：**v2.1.0**。

## 依赖

| 组件 | 版本 | 用途 |
|---|---|---|
| Go | 1.24+ | 服务端 / CLI |
| Git | 任意 | 克隆 |
| OpenSSL | 3.x | 证书（可选，向导也会生成） |

Linux 部署另需：`systemd`；可选 `cloudflared`、`nginx`。

## 安装 Go

```bash
curl -fsSL https://go.dev/dl/go1.24.5.linux-amd64.tar.gz -o /tmp/go.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf /tmp/go.tar.gz
echo 'export PATH=/usr/local/go/bin:$HOME/go/bin:$PATH' >> ~/.bashrc
source ~/.bashrc
go version
```

## 克隆与测试

```bash
git clone https://github.com/Contemporaries/lynx.git
cd lynx
go test ./...
go vet ./...
```

## 构建发布物

```bash
./deploy/build.sh
ls dist/
```

- `lynx-server-linux-amd64` / `lynx-server-linux-arm64`
- `lynx-client-linux-amd64` / `lynx-client-linux-arm64`
- `lynx-client-windows-amd64.exe`
- `SHA256SUMS`

## 模块代理（可选）

```bash
export GOPROXY=https://proxy.golang.org,direct
```
