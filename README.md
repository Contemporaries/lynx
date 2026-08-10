# Lynx

**English** | [中文](README.zh-CN.md)

Lynx **v2.3.0** is an encrypted TCP proxy: apps use local SOCKS5 / HTTP, and the client reaches the server over **direct mTLS** or **Cloudflare WebSocket**. The inner tunnel is TLS 1.3 with per-device mTLS.

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
- Graded traffic logs (`log.level`) and optional management API + separate Web UI

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

Client `mode`: `direct` / `wss` / `auto` (default: try WSS first, fall back to direct; while on direct, probe WSS in the background — runtime only, does not rewrite `client.json`). Logs print the active path (`path` / `via`). Full comparison: [docs/transport.md](docs/transport.md).

## Install (clone → install)

Recommended path on a Linux server (root). Needs Go **1.24+** to build, a Cloudflare CDN hostname for WSS, and a subscribe hostname with nginx HTTPS (443).

```bash
git clone https://github.com/Contemporaries/lynx.git
cd lynx
./deploy/build.sh
sudo ./lynx-wizard.sh
# same as: sudo ./install.sh
```

The wizard installs binaries, PKI, Cloudflare Tunnel, `server.json`, systemd units, and a client bundle. Then:

1. Add nginx subscribe reverse proxy: [deploy/nginx-subscribe.conf.example](deploy/nginx-subscribe.conf.example)
2. Back up `/etc/lynx/pki/ca.key`
3. Install the client package / run subscribe on each device

```bash
sudo lynx-wizard --show-subscribe
sudo systemctl status lynx-server lynx-cloudflared

# Client (from wizard package, or):
lynx-client -subscribe 'https://subscribe.example.com/_lynx/v1/subscribe/<token>' \
  -config /etc/lynx/client.json
```

Verify:

```bash
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
curl -x http://127.0.0.1:8080 https://ifconfig.me
```

Step-by-step checklist: [ONE_CLICK.md](ONE_CLICK.md). Config / Cloudflare / security: [docs/configuration.md](docs/configuration.md) · [docs/cloudflare.md](docs/cloudflare.md) · [docs/security.md](docs/security.md).

### Prebuilt binaries

**https://github.com/Contemporaries/lynx/releases** — `lynx-server` / `lynx-client` for linux amd64/arm64, Windows client, `SHA256SUMS`.

Windows client: [docs/windows.md](docs/windows.md).

## Documentation

| Doc | Contents |
|---|---|
| [ONE_CLICK.md](ONE_CLICK.md) | Clone → build → wizard install |
| [docs/configuration.md](docs/configuration.md) | nginx, server/client JSON |
| [docs/logging.md](docs/logging.md) | Traffic log levels |
| [docs/mgmt-api.md](docs/mgmt-api.md) | Optional management API |
| [docs/webui.md](docs/webui.md) | Separate Web UI binaries |
| [docs/cloudflare.md](docs/cloudflare.md) | Tunnel, DNS, Access, WSS |
| [docs/security.md](docs/security.md) | mTLS, tokens, limits |
| [docs/transport.md](docs/transport.md) | Direct vs WSS |
| [docs/development.md](docs/development.md) | Dev build / tests |
| [docs/windows.md](docs/windows.md) | Windows client |
| [docs/upgrade.md](docs/upgrade.md) | Upgrade and releases |
| [docs/uninstall.md](docs/uninstall.md) | Uninstall |

中文：[README.zh-CN.md](README.zh-CN.md) and `*.zh-CN.md` docs.

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
