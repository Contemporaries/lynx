# 开发

[English](development.md) | **中文**

从源码构建与测试 Lynx **v2.2.0**。

## 依赖

| 依赖 | 说明 |
|---|---|
| Go **1.24+** | 见 `go.mod` |
| Git | 克隆 |
| Linux 部署额外 | `systemd`；可选 `cloudflared`、`nginx` |

## 安装 Go

见 https://go.dev/dl/ 。确认：

```bash
go version
```

下载较慢时可设模块代理，例如：

```bash
export GOPROXY=https://goproxy.cn,direct
```

## 克隆与测试

```bash
git clone https://github.com/Contemporaries/lynx.git
cd lynx
go test ./...
```

## 发布风格二进制

```bash
./deploy/build.sh
```

产物在 `dist/`（linux amd64/arm64 服务端/客户端、Windows 客户端及脚本生成的校验和）。

## 本地运行（提纲）

1. 准备 `configs/server.json` / PKI（或实验用 `/etc/lynx`）
2. 运行 `lynx-server -config …`
3. 运行 `lynx-client -config …` 或 `-subscribe …`
4. 无 Cloudflare 时可本机访问 `ws_listen`；生产 WSS 见 [cloudflare.zh-CN.md](cloudflare.zh-CN.md)

## 相关

- [configuration.zh-CN.md](configuration.zh-CN.md)  
- [ONE_CLICK.zh-CN.md](../ONE_CLICK.zh-CN.md)  
