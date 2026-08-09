# Install: clone → build → wizard

**English** | [中文](ONE_CLICK.zh-CN.md)

Lynx **v2.2.0** on Linux: local SOCKS5/HTTP; subscribe via nginx **443**; optional direct mTLS **8443**; WSS via Cloudflare Tunnel.

## 1. Prerequisites

1. Go **1.24+** on the build machine (`go version`)
2. CDN hostname on Cloudflare (e.g. `cdn.example.com`) for WSS
3. Subscribe hostname with a public TLS cert + nginx (e.g. `subscribe.example.com` on **443** — not port **8443**)
4. Optional: open **TCP 8443** if you enable direct
5. Browser for `cloudflared tunnel login`

## 2. Clone and build

```bash
git clone https://github.com/Contemporaries/lynx.git
cd lynx
./deploy/build.sh
```

## 3. Install (wizard)

```bash
sudo ./lynx-wizard.sh
# equivalent: sudo ./install.sh
```

Typical prompts:

1. CDN hostname — e.g. `cdn.example.com`
2. Subscribe hostname — e.g. `subscribe.example.com`
3. Enable direct? (TCP 8443) — default no
4. Direct hostname if enabled — e.g. `direct.example.com` (**DNS only** / grey cloud)
5. First device name — default `laptop`
6. Optional Cloudflare Access Service Token

What gets installed:

- Binaries + `lynx-server` / `lynx-cloudflared` systemd units
- PKI under `/etc/lynx/pki` and `/etc/lynx/certs` — **back up `ca.key`**
- Tunnel: only `/_lynx/v1/connect` → `127.0.0.1:8080`
- Client bundle (subscribe URL; direct fields if enabled)

## 4. nginx subscribe

Add the location from [deploy/nginx-subscribe.conf.example](deploy/nginx-subscribe.conf.example), reload nginx.

```bash
sudo lynx-wizard --show-subscribe
sudo systemctl status lynx-server lynx-cloudflared
curl -sS http://127.0.0.1:8080/_lynx/v1/version
```

Expected:

```text
Cloudflare proxy entry: wss://cdn.example.com/_lynx/v1/connect
Subscribe URL: https://subscribe.example.com/_lynx/v1/subscribe/<token>
```

## 5. Client

Use the wizard-generated package, or:

```bash
sudo install -m 755 dist/lynx-client-linux-amd64 /usr/local/bin/lynx-client
sudo lynx-client -subscribe 'https://subscribe.example.com/_lynx/v1/subscribe/<token>' \
  -config /etc/lynx/client.json
```

More devices:

```bash
sudo lynx-wizard --add-device
```

## 6. Verify

```bash
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
curl -x http://127.0.0.1:8080 https://ifconfig.me
```

## Useful commands

```bash
sudo lynx-wizard --status
sudo lynx-wizard --show-subscribe
sudo lynx-wizard --add-device
sudo lynx-wizard --upgrade-server [tag]
sudo lynx-wizard --upgrade-client [tag]
sudo lynx-wizard --uninstall-client
sudo lynx-wizard --uninstall
```

Deep dives: [docs/configuration.md](docs/configuration.md) · [docs/cloudflare.md](docs/cloudflare.md) · [docs/security.md](docs/security.md) · [docs/transport.md](docs/transport.md).
