# Direct mTLS vs Cloudflare WSS

**English** | [中文](transport.zh-CN.md)

For Lynx **v2.2.0**. Two data-plane paths reach the server. Subscribe (nginx 443 path) is separate and does not replace either path.

Both end in inner TLS 1.3 + per-device mTLS. The CDN **cannot** read proxy plaintext; it can see hostname, volume, duration, and similar metadata.

## Paths

| | Direct mTLS | Cloudflare WSS |
|---|---|---|
| Path | Client → your public IP/name **:8443** → server | Client → Cloudflare → Tunnel → `127.0.0.1:8080` → server |
| Outer layer | TLS 1.3 + device cert (mTLS) | HTTPS/WSS (optional CF Access) + **inner** TLS 1.3 + device cert |
| Typical port | **8443** | **443** (CDN) |

## Trade-offs

| Dimension | Direct | WSS |
|---|---|---|
| **Latency / throughput** | Usually better (no CDN hop) | Cloudflare + Tunnel; often a bit slower |
| **Stability** | Needs public reachability and firewall allow for 8443 | Depends on Cloudflare / Tunnel; origin need not expose 8443 |
| **Network compatibility** | Some networks block odd ports or direct IPs | 443 + CDN often works better |
| **Exposure** | Public **TCP 8443** | Data plane can skip opening direct; subscribe still on nginx 443 |
| **Dependencies** | Direct hostname/IP reachable | Tunnel, CDN hostname, optional CF Access |
| **Metadata privacy** | Traffic goes to your server | Cloudflare sees connection metadata; **not** inner content |

## Client `mode`

In `client.json`:

| `mode` | Behavior |
|---|---|
| `"direct"` | Direct only |
| `"wss"` | WSS only |
| `"auto"` (default) | Try WSS first (~20s), then fall back to direct (~5s). If WSS succeeds, persist `mode=wss` into `client.json` |

One-click bundles with direct enabled usually start as `"mode": "auto"`.

## Choosing

- Prefer Cloudflare path / strict firewall → keep `auto` or set `wss`
- Prefer lowest latency on a stable 8443 → set `direct`
- Want probe-then-stick-to-WSS → `auto` (locks to `wss` after first WSS success)

More on Tunnel and Access: [cloudflare.md](cloudflare.md). Security implications: [security.md](security.md).

## Which path is active?

1. **Config**: `mode` is `direct` or `wss` → fixed. After a successful auto→WSS, the file usually shows `"mode": "wss"`.
2. **Logs**:  
   `auto: WSS ok, switched mode to wss…` → WSS and persisted.  
   `Cloudflare WSS unavailable, falling back to direct TLS` → that dial used direct.
3. **Outbound sockets**:

```bash
ss -tnp | grep -E 'lynx-client|8443'
```

- Destination **:8443** on the direct host → direct mTLS  
- Destination CDN host **:443** → Cloudflare WSS  
