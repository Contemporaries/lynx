# Windows 客户端

[English](windows.md) | **中文**

Lynx **v2.3.0** 单文件配置。

## 订阅并启动

```powershell
.\lynx-client-windows-amd64.exe -subscribe "https://subscribe.example.com/_lynx/v1/subscribe/<token>" -config .\client.json

.\lynx-client-windows-amd64.exe -config .\client.json
```

二进制见 [Releases](https://github.com/Contemporaries/lynx/releases)。

## client.json

首次可仅含：

```json
{
  "subscribe_url": "https://subscribe.example.com/_lynx/v1/subscribe/<token>",
  "socks_listen": "127.0.0.1:1080",
  "http_listen": "127.0.0.1:8080",
  "proxy_channels": 3
}
```

订阅成功后同文件含内联 PEM 与端点。模式与 Access：[configuration.zh-CN.md](configuration.zh-CN.md)、[transport.zh-CN.md](transport.zh-CN.md)、[cloudflare.zh-CN.md](cloudflare.zh-CN.md)。

## 验证

```powershell
curl.exe --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
curl.exe -x http://127.0.0.1:8080 https://ifconfig.me
```

浏览器/应用代理指向 `127.0.0.1:1080`（SOCKS5）或 `127.0.0.1:8080`（HTTP）。

## 安全提示

- 保管好 `client.json`（含设备私钥材料）
- 优先本机监听；非本机绑定需 `proxy_username` / `proxy_password`
- 见 [security.zh-CN.md](security.zh-CN.md)
