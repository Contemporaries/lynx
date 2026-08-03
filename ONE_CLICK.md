# One-click deploy (Linux)

**English** | [中文](ONE_CLICK.zh-CN.md)

For Lynx **v2.1.0**: local SOCKS5/HTTP; subscribe via nginx **443**; direct mTLS **8443**; WSS via Cloudflare Tunnel.

Deep dives: [docs/configuration.md](docs/configuration.md) · [docs/cloudflare.md](docs/cloudflare.md) · [docs/security.md](docs/security.md).

## Before you start

1. CDN hostname already on Cloudflare (e.g. `cdn.example.com`) for WSS
2. Subscribe hostname with a public certificate + nginx (e.g. `subscribe.example.com`, default **443** — do **not** use direct port **8443**)
3. Optional: open **TCP 8443** if you want direct acceleration
4. Browser available for `cloudflared tunnel login`

## Deploy server

```bash
./deploy/build.sh
sudo ./lynx-wizard.sh
# equivalent: sudo ./install.sh
```

Wizard prompts (typical):

1. CDN hostname — e.g. `cdn.example.com`
2. Subscribe hostname — e.g. `subscribe.example.com`
3. Enable direct? (open TCP 8443) — default no
4. Direct hostname if enabled — e.g. `direct.example.com` (**DNS only** / grey cloud)
5. First device name — default `laptop`
6. Optional Cloudflare Access Service Token (Client-Id / Secret)

What the wizard sets up:

- `lynx-server` / `lynx-cloudflared` (and related paths under `/etc/lynx`)
- PKI under `/etc/lynx/pki` and `/etc/lynx/certs` — **back up `ca.key`**
- Tunnel ingress: only `/_lynx/v1/connect` → `127.0.0.1:8080`
- Client bundle with subscribe URL (and direct fields if enabled)

Then add nginx for subscribe: [deploy/nginx-subscribe.conf.example](deploy/nginx-subscribe.conf.example).

```bash
sudo lynx-wizard --show-subscribe
sudo systemctl status lynx-server lynx-cloudflared
```

Expected prints:

```text
Cloudflare proxy entry: wss://cdn.example.com/_lynx/v1/connect
Subscribe URL: https://subscribe.example.com/_lynx/v1/subscribe/<token>
```

If direct is on, also ensure security group TCP 8443 and grey-cloud DNS for the direct name.

## Add a device

```bash
sudo lynx-wizard --add-device
# or menu option in lynx-wizard.sh
```

## Install client (Linux)

Use the generated package / instructions from the wizard (typically install binary + `client.json` + systemd `lynx-client`).

Or manually:

```bash
sudo install -m 755 lynx-client-linux-amd64 /usr/local/bin/lynx-client
sudo lynx-client -subscribe 'https://subscribe.example.com/_lynx/v1/subscribe/<token>' \
  -config /etc/lynx/client.json
```

## Verify

```bash
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
curl -x http://127.0.0.1:8080 https://ifconfig.me
```

## Useful wizard commands

```bash
sudo lynx-wizard --status
sudo lynx-wizard --show-subscribe
sudo lynx-wizard --add-device
sudo lynx-wizard --upgrade-server [tag]
sudo lynx-wizard --upgrade-client [tag]
sudo lynx-wizard --uninstall-client
sudo lynx-wizard --uninstall
```

Interactive menu: first deploy, add device, status, subscribe probe, upgrades, uninstall.
