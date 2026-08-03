# Security

**English** | [中文](security.zh-CN.md)

Threat model, trust boundaries, and controls for Lynx **v2.1.0**.

## What Lynx is (and is not)

- Encrypted **TCP** proxy: apps use local SOCKS5 / HTTP CONNECT.
- **Not** a system VPN: no TUN, no default-route or system-DNS hijack.
- No UDP / QUIC / SOCKS UDP ASSOCIATE.
- Not independently audited.

## Trust boundaries

```text
[App] --local--> [Lynx client] ==encrypted tunnel==> [Lynx server] --TCP--> [Internet target]
                      |                                    |
                 client.json                         server.json + PKI
                 (PEM + tokens)                      (CA, device certs, tokens)
```

| Segment | Protection |
|---|---|
| App ↔ client | Localhost by default; bind off-loopback **requires** proxy username/password |
| Client ↔ server (direct) | TLS 1.3 + mutual client cert; ALPN |
| Client ↔ server (WSS) | Outer HTTPS/WSS (optional CF Access) + **inner** TLS 1.3 + client cert |
| Server ↔ target | Plain TCP as requested by the client (subject to private-net policy) |

## Device authorization (mTLS)

1. Wizard/PKI issues a **per-device** client certificate signed by your Lynx CA.
2. Server `clients.<name>.certificate_sha256` must match the cert fingerprint (lowercase hex, no colons).
3. `enabled: false` rejects sessions and subscribe for that device.
4. Unauthorized fingerprint: connection refused; server may log  
   `rejected unauthorized client fingerprint=…`

Direct path uses client certs on the outer TLS. WSS path uses them on the **inner** TLS after the WebSocket is up (`ws_inner_server_name` defaults to `lynx.internal`).

## Subscribe tokens

URL shape:

```text
https://subscribe.example.com/_lynx/v1/subscribe/<token>
```

- Token is a **secret** (`openssl rand -hex 32` recommended). Treat like a password.
- Unknown or disabled token → opaque **HTTP 404** (no existence leak in the body).
- Response includes `ca_pem` / `cert_pem` / `key_pem` — full device material. Use HTTPS only; response sets `Cache-Control: no-store`.
- Rate limits (defaults): 30 req/min/IP, 10 req/min/token → HTTP 429.
- Client source IP for limits prefers `CF-Connecting-IP`, then `X-Forwarded-For` (configure nginx accordingly).

If no `subscribe_token` is configured for any client, subscribe GETs always 404 and the server warns in logs.

**Rotate** a leaked token by changing `subscribe_token` (and redistributing the URL) or set `enabled: false`.

## Cloudflare visibility

| Cloudflare **can** see | Cloudflare **cannot** see |
|---|---|
| CDN hostname, client public IP, timing, bytes | Inner TLS plaintext |
| That a WSS session exists to `/_lynx/v1/connect` | SOCKS target host/port, proxied HTTP bodies |
| Access auth success/failure (if enabled) | Direct-path traffic (bypasses CF) |

Optional **Access Service Token** adds an edge gate before WSS; Lynx still requires device mTLS inside. See [cloudflare.md](cloudflare.md).

## Private network protection

Default `allow_private_networks: false` blocks proxying to:

- Loopback, unspecified, multicast, link-local
- RFC1918 private ranges

Denied dials fail with an error like `target address is not allowed`. Set `true` only if you intentionally need LAN/VPN targets through the server.

## Rate and session limits

Configured under `security` in `server.json` (defaults shown):

| Field | Default | Role |
|---|---|---|
| `max_sessions_per_certificate` | 4 | Concurrent tunnels per device cert |
| `max_sessions_per_source_ip` | 8 | Concurrent tunnels per client IP |
| `max_total_sessions` | 256 | Global |
| `max_flows_per_certificate` | 512 | Concurrent flows per cert |
| `max_new_flows_per_second` | 50 | New flow rate |
| `handshake_timeout_seconds` | 10 | TLS/session handshake |
| `session_idle_timeout_seconds` | 300 | Idle session |
| `flow_idle_timeout_seconds` | 600 | Idle flow |
| `max_subscribe_per_ip_per_min` | 30 | Subscribe rate / IP |
| `max_subscribe_per_token_per_min` | 10 | Subscribe rate / token |

Also: `max_proxy_flows_per_session` (default 256), `proxy_dial_timeout_seconds` (15).

## Local proxy auth

- Prefer `127.0.0.1` for `socks_listen` / `http_listen`.
- If you bind a non-loopback address, Lynx **requires** `proxy_username` and `proxy_password`.

## Key material on disk

| Path | Sensitivity |
|---|---|
| `/etc/lynx/pki/ca.key` | **Critical** — CA private key; back up offline |
| `/etc/lynx/pki/*.key`, device PEMs in `client.json` | Device identity |
| `/etc/lynx/server.json` subscribe tokens | Capability to fetch device PEMs |
| `/etc/lynx/cloudflared/*.json` | Tunnel credentials |

Upgrades do **not** rotate device certificates. Uninstall removes `/etc/lynx` on full server uninstall — back up first.

## Operational checklist

1. Subscribe only over HTTPS (nginx with a real cert).
2. Do not share subscribe URLs or `client.json` PEMs.
3. Keep direct hostname **DNS only** if using `:8443`; open security group TCP 8443 only as needed.
4. Prefer CF Access on the CDN hostname for WSS when the zone is exposed broadly.
5. Leave `allow_private_networks` false unless required.
6. Disable or rotate tokens for lost devices; remove or disable `clients` entries.

## Related

- [configuration.md](configuration.md) — field reference and setup steps  
- [cloudflare.md](cloudflare.md) — Tunnel, DNS, Access  
- [transport.md](transport.md) — direct vs WSS  
