# Development

**English** | [中文](development.zh-CN.md)

Build and test Lynx **v2.2.2** from source.

## Requirements

| Dependency | Notes |
|---|---|
| Go **1.24+** | `go.mod` |
| Git | Clone |
| Linux deploy extras | `systemd`; optional `cloudflared`, `nginx` |

No external Go module dependencies beyond the standard library (see `go.mod`).

## Install Go

Follow https://go.dev/dl/ for your OS. Confirm:

```bash
go version
```

If downloads are slow, set a module proxy (example for mainland China):

```bash
export GOPROXY=https://goproxy.cn,direct
```

## Clone and test

```bash
git clone https://github.com/Contemporaries/lynx.git
cd lynx
go test ./...
```

## Release-style binaries

```bash
./deploy/build.sh
```

Artifacts land under `dist/` (server/client for linux amd64/arm64, Windows client, checksums as produced by the script).

## Local run (sketch)

1. Prepare `configs/server.json` / PKI paths (or copy a lab layout under `/etc/lynx`).
2. Run `lynx-server -config …`
3. Run `lynx-client -config …` or `-subscribe …`
4. For WSS without Cloudflare, you can still hit `ws_listen` locally; production WSS expects Tunnel as in [cloudflare.md](cloudflare.md).

## Related

- [configuration.md](configuration.md) — runtime config  
- [ONE_CLICK.md](../ONE_CLICK.md) — production wizard  
