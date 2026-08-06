# Lynx

**English** | [中文](README.zh-CN.md)

Lynx **v2.1.0** is an encrypted TCP proxy: apps use local SOCKS5 / HTTP, and the client reaches the server over **direct mTLS** or **Cloudflare WebSocket**. The inner tunnel is TLS 1.3 with per-device mTLS.

It is **not** a system VPN: no TUN device, no default route or system DNS changes.

```text
Application
  ├─ SOCKS5 127.0.0.1:1080
  └─ HTTP   127.0.0.1:8080
             │
        Lynx client (single-file client.json)
             │
    ┌────────┴─────────┐
    │                  │
Direct mTLS :8443   Cloudflare WSS
    │                  │
    └──── Lynx server ──── Internet
              ▲
         Inner TLS 1.3 + mTLS
```

## Features

- Single-file client: `client.json` (`subscribe_url` and/or inline `certificate` / `key` / `certificate_authority`)
- Subscribe: `https://subscribe.example.com/_lynx/v1/subscribe/<token>` (nginx **443** path)
- Direct: `direct_listen` default **:8443** (mTLS)
- Data plane: Cloudflare WSS + end-to-end inner TLS (CDN cannot read proxy plaintext)
- Local SOCKS5 (TCP CONNECT + UDP ASSOCIATE) and HTTP / HTTP CONNECT
- Multiplexed connection pool; private destinations blocked by default
- Non-loopback local listen requires proxy authentication
- Linux / Windows CLI; systemd one-click deploy

## Non-goals

- System VPN / global routing
- QUIC as client↔server transport (Cloudflare Tunnel may still use QUIC to the origin)
- Independent security audit (not done yet)

## Ports

| Use | Default |
|---|---|
| Subscribe (nginx → origin `127.0.0.1:8080`) | HTTPS **443** (`/_lynx/v1/subscribe/`) |
| Direct mTLS | TCP **8443** |
| Server WebSocket origin | `127.0.0.1:8080` (Tunnel allows connect only) |
| Client SOCKS5 / HTTP | `127.0.0.1:1080` / `127.0.0.1:8080` |

Subscribe and direct must not share the same port.

## Direct vs WSS (summary)

| | Direct mTLS | Cloudflare WSS |
|---|---|---|
| Path | Client → public **:8443** → server | Client → Cloudflare Tunnel → localhost origin |
| Latency / throughput | Usually better | Extra CDN hop; often a bit slower |
| Firewall friendliness | Needs 8443 reachable | Uses 443; often easier |
| Exposure | Open TCP **8443** | Direct port can stay closed |
| Metadata | Straight to your server | CF sees connection metadata; cannot decrypt inner TLS |

Client `mode`: `direct` / `wss` / `auto` (default: try direct, fall back to WSS). Full comparison: [docs/transport.md](docs/transport.md).

## Download

**https://github.com/Contemporaries/lynx/releases**

| Artifact | Notes |
|---|---|
| `lynx-server-linux-amd64` / `arm64` | Server |
| `lynx-client-linux-amd64` / `arm64` | Linux client |
| `lynx-client-windows-amd64.exe` | Windows client |
| `SHA256SUMS` | Checksums |

## Documentation

| Doc | Contents |
|---|---|
| [ONE_CLICK.md](ONE_CLICK.md) | Linux one-click deploy |
| [docs/configuration.md](docs/configuration.md) | Config steps, nginx, server/client JSON |
| [docs/cloudflare.md](docs/cloudflare.md) | Tunnel, DNS, Access, WSS path |
| [docs/security.md](docs/security.md) | mTLS, tokens, limits, CDN visibility |
| [docs/transport.md](docs/transport.md) | Direct vs WSS |
| [docs/development.md](docs/development.md) | Build from source |
| [docs/windows.md](docs/windows.md) | Windows client |
| [docs/upgrade.md](docs/upgrade.md) | Upgrade and releases |
| [docs/uninstall.md](docs/uninstall.md) | Uninstall |

中文文档：见各文件的 `*.zh-CN.md`（例如 [README.zh-CN.md](README.zh-CN.md)）。

## Quick start

### Build

```bash
# Go 1.24+
go test ./...
./deploy/build.sh
```

See [docs/development.md](docs/development.md).

### Server (recommended: wizard)

```bash
./deploy/build.sh
sudo ./lynx-wizard.sh
```

The wizard configures Cloudflare Tunnel, PKI, `server.json`, and a client bundle. Subscribe URL shape:

```text
https://subscribe.example.com/_lynx/v1/subscribe/<token>
```

nginx snippet: [deploy/nginx-subscribe.conf.example](deploy/nginx-subscribe.conf.example). Back up `/etc/lynx/pki/ca.key`.

Detailed steps: [docs/configuration.md](docs/configuration.md) · Cloudflare: [docs/cloudflare.md](docs/cloudflare.md).

### Server config (essentials)

```json
{
  "direct_listen": ":8443",
  "public_base_url": "https://subscribe.example.com",
  "cdn_base_url": "https://cdn.example.com",
  "ws_path": "/_lynx/v1/connect",
  "subscribe_path_prefix": "/_lynx/v1/subscribe/",
  "clients": {
    "laptop": {
      "certificate_sha256": "<fingerprint>",
      "enabled": true,
      "subscribe_token": "<token>",
      "cert_file": "/etc/lynx/pki/laptop.crt",
      "key_file": "/etc/lynx/pki/laptop.key"
    }
  }
}
```

Full samples: [configs/server.json](configs/server.json), [configs/client.json](configs/client.json).

### Client

```bash
lynx-client -subscribe 'https://subscribe.example.com/_lynx/v1/subscribe/<token>' \
  -config /etc/lynx/client.json

lynx-client -config /etc/lynx/client.json
```

Minimal config:

```json
{
  "subscribe_url": "https://subscribe.example.com/_lynx/v1/subscribe/<token>"
}
```

After a successful subscribe, the same file receives inline PEMs. Verify:

```bash
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
curl -x http://127.0.0.1:8080 https://ifconfig.me
```

Windows: [docs/windows.md](docs/windows.md).

## Upgrade / uninstall

- Upgrade: [docs/upgrade.md](docs/upgrade.md)
- Uninstall: [docs/uninstall.md](docs/uninstall.md)

```bash
sudo ./deploy/upgrade-server.sh
sudo ./deploy/upgrade-client.sh
sudo ./deploy/uninstall-client.sh
sudo lynx-wizard --uninstall
```

## Security (short)

- Device auth: client certificate SHA-256 fingerprint + `enabled`
- Subscribe token is a secret; rotate `subscribe_token` or disable the device if leaked
- Rate limits: server `security` block
- Cloudflare sees hostname, client IP, duration, and volume — not inner TLS plaintext

Full write-up: [docs/security.md](docs/security.md).

## License

[MIT](LICENSE)
