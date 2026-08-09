# Cloudflare setup

**English** | [中文](cloudflare.zh-CN.md)

How Lynx uses Cloudflare Tunnel for the WSS data path, DNS, optional Access, and how connections are secured end to end.

## Role of Cloudflare

| Traffic | Through Cloudflare? |
|---|---|
| Data plane WSS (`/_lynx/v1/connect`) | **Yes** — Tunnel → origin `127.0.0.1:8080` |
| Subscribe (`/_lynx/v1/subscribe/`) | **No** — your nginx on the subscribe host (HTTPS 443) |
| Direct mTLS (`:8443`) | **No** — DNS should be **DNS only** (grey cloud) if you enable direct |

Cloudflare terminates the public HTTPS/WSS edge. Inside the tunnel, Lynx still runs **inner TLS 1.3 + per-device mTLS**, so the CDN cannot read proxy plaintext (destinations or payloads).

## Prerequisites

1. A hostname on a zone already in Cloudflare (e.g. `cdn.example.com`) for WSS.
2. Ability to run `cloudflared tunnel login` in a browser (one-time cert under `/root/.cloudflared/cert.pem`).
3. Optional: Cloudflare Access Service Token if you protect the CDN hostname with Access.

The one-click wizard (`lynx-wizard` / `install.sh`) installs `cloudflared`, creates the tunnel, writes config, and enables `lynx-cloudflared`. Manual steps below match what the wizard does.

## Wizard flow (summary)

On first server install the wizard asks for:

1. **CDN hostname** — e.g. `cdn.example.com`
2. **Subscribe hostname** — e.g. `subscribe.example.com` (nginx 443; not port 8443)
3. **Enable direct?** — open TCP 8443 if yes
4. First device name
5. Whether **CF Access Service Token** is already required on the CDN host

After confirm it will:

1. Install `cloudflared` from Cloudflare’s package repo
2. Run `cloudflared tunnel login` if needed
3. Create a tunnel named like `lynx-<host>-<timestamp>`
4. Copy credentials to `/etc/lynx/cloudflared/<uuid>.json`
5. Route DNS: `cloudflared tunnel route dns --overwrite-dns <uuid> <cdn_host>`
6. Write `/etc/lynx/cloudflared.yml` and start `lynx-cloudflared`

## Tunnel config

Default path written by the wizard: `/etc/lynx/cloudflared.yml`

```yaml
tunnel: <tunnel-uuid>
credentials-file: /etc/lynx/cloudflared/<tunnel-uuid>.json
protocol: quic
ingress:
  - hostname: cdn.example.com
    path: ^/_lynx/v1/connect$
    service: http://127.0.0.1:8080
  - service: http_status:404
```

Repo sample: [deploy/cloudflared-config.yml](../deploy/cloudflared-config.yml).

Important:

- Only the **connect** path is proxied. Everything else on that hostname returns 404 from the tunnel.
- Subscribe must **not** be routed through this ingress; use nginx → `127.0.0.1:8080` on the subscribe host.
- Origin is plain HTTP on localhost; public TLS is at the Cloudflare edge (and again inside Lynx for the inner tunnel).

systemd unit (wizard): `lynx-cloudflared` runs:

```text
cloudflared --no-autoupdate --config /etc/lynx/cloudflared.yml tunnel run
```

## DNS

| Hostname | Recommended Cloudflare proxy | Purpose |
|---|---|---|
| `cdn.example.com` | **Proxied** (orange cloud) via Tunnel CNAME | WSS |
| `subscribe.example.com` | Your choice (often proxied or separate cert) | nginx subscribe |
| `direct.example.com` | **DNS only** (grey cloud) | Direct mTLS to origin `:8443` |

If `tunnel route dns` fails, delete conflicting A/AAAA/CNAME for the CDN name, then:

```bash
sudo cloudflared tunnel route dns <tunnel-uuid> cdn.example.com
```

After deploy the wizard prints:

```text
Cloudflare proxy entry: wss://cdn.example.com/_lynx/v1/connect
```

## Connection path (WSS)

```text
App → Lynx client (SOCKS5/HTTP)
        → TLS to Cloudflare (cdn.example.com:443)
        → optional CF Access headers
        → WebSocket upgrade POST/GET /_lynx/v1/connect
        → Tunnel → http://127.0.0.1:8080
        → Lynx server accepts WS
        → Inner TLS 1.3 + client cert (SNI lynx.internal)
        → Multiplexed proxy flows
```

Client fields (after subscribe / wizard bundle):

```json
{
  "mode": "wss",
  "ws_url": "wss://cdn.example.com/_lynx/v1/connect",
  "ws_inner_server_name": "lynx.internal",
  "cf_access_client_id": optional,
  "cf_access_client_secret": optional
}
```

With direct enabled, bundles usually use `"mode": "auto"` plus `direct_addr`.

### Timeouts and `auto`

- Direct dial: ~5s
- WSS dial: ~20s
- `auto`: try WSS first; on failure use direct and keep probing WSS every ~15s.
  Logs include the active path, e.g. `transport: path=wss via=wss://…` or
  `auto: path changed wss → direct (…) via=host:8443`. Does **not** rewrite `client.json`.

## Cloudflare Access (optional)

Protect `cdn.example.com` with an Access application that allows a **Service Token**.

1. Cloudflare Zero Trust → Access → Service Auth → create Service Token  
   → Client ID + Client Secret.
2. Access application for `cdn.example.com` (or the connect path), policy: Service Token.
3. Give the wizard the ID/secret when asked, **or** put them in `client.json`:

```json
"cf_access_client_id": "...",
"cf_access_client_secret": "..."
```

The client sends:

```http
CF-Access-Client-Id: ...
CF-Access-Client-Secret: ...
```

on the WSS handshake. The Lynx **server does not validate** Access; Cloudflare does at the edge. Subscribe tokens and device certs are separate.

Access tokens are typically placed in the **client package** by the wizard; they are **not** pushed by the subscribe HTTP API.

## Verify

```bash
sudo systemctl status lynx-server lynx-cloudflared
curl -sS http://127.0.0.1:8080/_lynx/v1/version
# From a machine that can reach the CDN (expect WS/upgrade related response or 4xx without proper client):
curl -sSI https://cdn.example.com/_lynx/v1/connect
```

Client logs after a good pool:

```text
proxy transport ready: N/M encrypted channels
```

See [transport.md](transport.md) to tell direct vs WSS, and [security.md](security.md) for what Cloudflare can and cannot see.

## State files

| Path | Purpose |
|---|---|
| `/etc/lynx/cloudflared.yml` | Tunnel ingress |
| `/etc/lynx/cloudflared/<uuid>.json` | Tunnel credentials |
| `/etc/lynx/wizard.env` | Wizard state (`CDN_HOST`, `TUNNEL_ID`, …) |
| `/root/.cloudflared/cert.pem` | Cloudflare account login cert (root) |

Uninstalling Lynx removes Lynx units and `/etc/lynx` but **does not** remove the `cloudflared` package or Cloudflare-side tunnel objects; clean those in the Zero Trust / Cloudflare dashboard if needed.
