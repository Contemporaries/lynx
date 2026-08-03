# Windows 客户端

## 单文件配置

```powershell
# 仅订阅 URL（会写回 client.json，内含 inline PEM）
.\lynx-client-windows-amd64.exe -subscribe "https://subscribe.example.com/_lynx/v1/subscribe/<token>" -config .\client.json

# 之后日常启动
.\lynx-client-windows-amd64.exe -config .\client.json
```

`client.json` 示例（首次可只有 subscribe_url）：

```json
{
  "subscribe_url": "https://subscribe.example.com/_lynx/v1/subscribe/<token>",
  "socks_listen": "127.0.0.1:1080",
  "http_listen": "127.0.0.1:8080",
  "proxy_channels": 3
}
```

拉取成功后文件内会出现 `certificate` / `key` / `certificate_authority` 字段，**不再需要** 单独的 `.crt` / `.key` 文件。

验证：

```powershell
curl.exe --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
```
