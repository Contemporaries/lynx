# Configuration

**English** | [中文](configuration.zh-CN.md)

Step-by-step setup for Lynx **v2.2.0**: ports, nginx subscribe, server/client JSON, and verification. For Tunnel/Access detail see [cloudflare.md](cloudflare.md). For controls and threat model see [security.md](security.md).

## Architecture (ports)

| Role | Default | Notes |
|---|---|---|
| Subscribe (public) | HTTPS **443** path `/_lynx/v1/subscribe/` | nginx → `127.0.0.1:8080` |
| Direct mTLS | TCP **8443** | Must **not** share the subscribe port |
| Server WS + subscribe origin | `127.0.0.1:8080` | Tunnel only allows connect path |
| Client SOCKS5 | `127.0.0.1:1080` | TCP CONNECT + UDP ASSOCIATE |
| Client HTTP proxy | `127.0.0.1:8080` | Local to the client machine |

```text
Apps → SOCKS5/HTTP (localhost)
         → Lynx client
              ├─ direct TLS → direct.example.com:8443
              └─ WSS → cdn.example.com/_lynx/v1/connect → Tunnel → :8080
         Lynx server also serves subscribe on :8080 (via nginx on 443)
```

## Recommended path: clone → install

On a Linux server (root):

```bash
git clone https://github.com/Contemporaries/lynx.git
cd lynx
./deploy/build.sh
sudo ./lynx-wizard.sh
# or: sudo ./install.sh
```

You will be asked for CDN hostname, subscribe hostname, optional direct, first device name, optional CF Access token. The wizard installs binaries, PKI, `server.json`, `lynx-server`, Cloudflare Tunnel, and a client bundle.

Full checklist: [ONE_CLICK.md](../ONE_CLICK.md). Cloudflare specifics: [cloudflare.md](cloudflare.md).

Afterward configure **nginx** on the subscribe host (below), then install the client package on each device.

## Step A — nginx subscribe (required for public subscribe URLs)

1. Point `subscribe.example.com` at this server (or the host that can reach `127.0.0.1:8080` on the Lynx box — usually same machine).
2. Obtain a public TLS certificate for that name (certbot, etc.).
3. Add the location **before** a catch-all `/` (sample: [deploy/nginx-subscribe.conf.example](../deploy/nginx-subscribe.conf.example)):

```nginx
location ^~ /_lynx/v1/subscribe {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header CF-Connecting-IP $remote_addr;
    proxy_connect_timeout 10s;
    proxy_send_timeout 30s;
    proxy_read_timeout 30s;
    proxy_buffering off;
    add_header Cache-Control "no-store" always;
}
```

4. Reload nginx. Self-check:

```bash
curl -sS http://127.0.0.1:8080/_lynx/v1/version
sudo lynx-wizard --show-subscribe
# Public (needs valid token):
curl -sS "https://subscribe.example.com/_lynx/v1/subscribe/<token>" | head
```

`public_base_url` in `server.json` must be the HTTPS origin clients use, e.g. `https://subscribe.example.com` (no `:8443`).

## Step B — server.json

Runtime path: `/etc/lynx/server.json`. Example: [configs/server.json](../configs/server.json).

### Core fields

| Field | Meaning |
|---|---|
| `direct_listen` | Direct mTLS listen. `:8443` public; `127.0.0.1:8443` if direct disabled |
| `ws_listen` | Origin for WS + subscribe (default `127.0.0.1:8080`) |
| `ws_path` | Default `/_lynx/v1/connect` |
| `public_base_url` | External subscribe base (`https://subscribe.example.com`) |
| `cdn_base_url` | Used to build client `ws_url` (`https://cdn.example.com`) |
| `subscribe_path_prefix` | Default `/_lynx/v1/subscribe/` |
| `cert_file` / `key_file` | Server TLS material for direct/inner |
| `client_ca_file` | CA that issued device certs |
| `clients.<name>` | Per-device auth + subscribe material |
| `allow_private_networks` | Default `false` — block RFC1918 etc. |
| `security` | Rate/session limits — see [security.md](security.md) |

### Per-client entry

```json
"laptop": {
  "certificate_sha256": "<sha256 of client cert>",
  "enabled": true,
  "subscribe_token": "<random hex>",
  "cert_file": "/etc/lynx/pki/laptop.crt",
  "key_file": "/etc/lynx/pki/laptop.key"
}
```

Add devices with:

```bash
sudo lynx-wizard --add-device
```

Restart after manual edits:

```bash
sudo systemctl restart lynx-server
```

## Step C — Cloudflare Tunnel

Wizard default config `/etc/lynx/cloudflared.yml` proxies only:

```text
cdn.example.com + path ^/_lynx/v1/connect$  →  http://127.0.0.1:8080
```

Enable and check:

```bash
sudo systemctl enable --now lynx-cloudflared
sudo systemctl status lynx-cloudflared
```

Details, DNS colours, Access tokens: [cloudflare.md](cloudflare.md).

## Step D — direct mTLS (optional)

1. Security group / firewall: allow **TCP 8443** from clients.
2. DNS for `direct.example.com`: **DNS only** (grey cloud), A/AAAA to the server.
3. `direct_listen`: `:8443`.
4. Client has `direct_addr` / `direct_server_name` and usually `mode: "auto"`.

## Step E — client.json

Single-file config. Minimal before first subscribe:

```json
{
  "subscribe_url": "https://subscribe.example.com/_lynx/v1/subscribe/<token>"
}
```

Or use the wizard-generated bundle (already contains PEM + endpoints).

### Important client fields

| Field | Default | Meaning |
|---|---|---|
| `subscribe_url` | — | Profile fetch URL |
| `mode` | `auto` | `direct` / `wss` / `auto` (auto: WSS first, then direct; WSS success → persist `wss`) |
| `direct_addr` | — | e.g. `direct.example.com:8443` |
| `direct_server_name` | — | TLS SNI for direct |
| `ws_url` | from subscribe | e.g. `wss://cdn.example.com/_lynx/v1/connect` |
| `ws_inner_server_name` | `lynx.internal` | Inner mTLS SNI |
| `certificate` / `key` / `certificate_authority` | from subscribe | Inline PEM |
| `cf_access_client_id` / `cf_access_client_secret` | optional | Access service token |
| `socks_listen` | `127.0.0.1:1080` | Empty disables; supports CONNECT + UDP ASSOCIATE |
| `http_listen` | `127.0.0.1:8080` | TCP only (no UDP) |
| `proxy_channels` | 3 (max 8) | Mux pool size |
| `proxy_username` / `proxy_password` | — | Required if listen is non-local |
| `ping_interval_seconds` | 20 | |
| `pong_timeout_misses` | 3 | |
| `subscribe_refresh_seconds` | 0 = off | Periodic re-subscribe |

Example: [configs/client.json](../configs/client.json). Transport modes: [transport.md](transport.md).

### Run client

```bash
lynx-client -subscribe 'https://subscribe.example.com/_lynx/v1/subscribe/<token>' \
  -config /etc/lynx/client.json

lynx-client -config /etc/lynx/client.json
```

Linux package install usually ships a systemd unit `lynx-client`.

### Verify

```bash
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
curl -x http://127.0.0.1:8080 https://ifconfig.me
```

Windows: [windows.md](windows.md).

## Manual server layout (no wizard)

1. Build: `./deploy/build.sh`
2. Install `lynx-server` to `/usr/local/bin`, create `/etc/lynx`, generate CA + server + device certs (or copy wizard PKI layout under `/etc/lynx/certs` and `/etc/lynx/pki`).
3. Write `server.json`, start server listening on `ws_listen` and `direct_listen`.
4. Install and configure `cloudflared` as in [cloudflare.md](cloudflare.md).
5. Configure nginx (Step A).
6. Distribute client config or subscribe URLs.

## Related

| Doc | Topic |
|---|---|
| [ONE_CLICK.md](../ONE_CLICK.md) | Wizard checklist |
| [cloudflare.md](cloudflare.md) | Tunnel, DNS, Access, WSS path |
| [security.md](security.md) | mTLS, tokens, limits, CDN visibility |
| [transport.md](transport.md) | Direct vs WSS |
| [upgrade.md](upgrade.md) / [uninstall.md](uninstall.md) | Lifecycle |
