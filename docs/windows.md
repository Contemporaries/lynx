# Windows client

**English** | [中文](windows.zh-CN.md)

Lynx **v2.1.0** single-file client config on Windows.

## Subscribe and start

```powershell
.\lynx-client-windows-amd64.exe -subscribe "https://subscribe.example.com/_lynx/v1/subscribe/<token>" -config .\client.json

.\lynx-client-windows-amd64.exe -config .\client.json
```

Download builds from [Releases](https://github.com/Contemporaries/lynx/releases).

## client.json

Minimal before first subscribe:

```json
{
  "subscribe_url": "https://subscribe.example.com/_lynx/v1/subscribe/<token>",
  "socks_listen": "127.0.0.1:1080",
  "http_listen": "127.0.0.1:8080",
  "proxy_channels": 3
}
```

After subscribe, the same file contains inline `certificate` / `key` / `certificate_authority` and endpoint fields. Modes and Cloudflare Access: [configuration.md](configuration.md), [transport.md](transport.md), [cloudflare.md](cloudflare.md).

## Verify

```powershell
curl.exe --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
curl.exe -x http://127.0.0.1:8080 https://ifconfig.me
```

Point browser or app proxy settings at `127.0.0.1:1080` (SOCKS5) or `127.0.0.1:8080` (HTTP).

## Security notes

- Keep `client.json` private (device key material).
- Prefer loopback listens; non-local binds require `proxy_username` / `proxy_password`.
- See [security.md](security.md).
