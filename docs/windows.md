# Windows 客户端

适用 Lynx **v2.1.0** 单文件配置。

## 订阅并启动

```powershell
.\lynx-client-windows-amd64.exe -subscribe "https://subscribe.example.com/_lynx/v1/subscribe/<token>" -config .\client.json

.\lynx-client-windows-amd64.exe -config .\client.json
```

## client.json

首次可仅含订阅地址：

```json
{
  "subscribe_url": "https://subscribe.example.com/_lynx/v1/subscribe/<token>",
  "socks_listen": "127.0.0.1:1080",
  "http_listen": "127.0.0.1:8080",
  "proxy_channels": 3
}
```

订阅成功后，同文件会包含内联 `certificate` / `key` / `certificate_authority`。

## 验证

```powershell
curl.exe --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
```

在浏览器或其他软件中将代理设为 `127.0.0.1:1080`（SOCKS5）或 `127.0.0.1:8080`（HTTP）。
