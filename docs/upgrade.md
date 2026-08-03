# Upgrade and release

**English** | [中文](upgrade.zh-CN.md)

Lynx **v2.1.0** upgrade paths and how releases are published.

## Release artifacts

GitHub Releases (`Contemporaries/lynx`) typically include:

- `lynx-server-linux-amd64` / `arm64`
- `lynx-client-linux-amd64` / `arm64`
- `lynx-client-windows-amd64.exe`
- `SHA256SUMS`

## Publish a release (maintainers)

1. Ensure `main` is green and version strings in wizard/`install.sh` match the tag.
2. Tag and push (example):

```bash
git tag v2.1.0
git push origin v2.1.0
```

3. CI (`.github/workflows/release.yml`) builds CLI artifacts and attaches them to the release.

## Upgrade server (installed via wizard/scripts)

```bash
sudo ./deploy/upgrade-server.sh
# or pin a tag:
sudo ./deploy/upgrade-server.sh v2.1.0
sudo lynx-wizard --upgrade-server [tag]
```

Typical behavior: download Release → verify `SHA256SUMS` → backup under `/var/backups/lynx/` (and `/etc/lynx`) → replace `/usr/local/bin/lynx-server` → restart units.

**Device certificates are not rotated** by upgrades. PKI and `server.json` remain unless you change them.

## Upgrade client

```bash
sudo ./deploy/upgrade-client.sh
sudo lynx-wizard --upgrade-client [tag]
```

Replaces `lynx-client` and restarts `lynx-client` when managed by systemd.

## Manual upgrade

1. Download the matching binary + `SHA256SUMS` from Releases; verify checksum.
2. Install over `/usr/local/bin/lynx-server` or `lynx-client`.
3. `sudo systemctl restart lynx-server` / `lynx-client` / `lynx-cloudflared` as needed.

## Rollback

Restore binaries (and optionally `/etc/lynx`) from `/var/backups/lynx/`, then restart services.

## After upgrade checklist

```bash
lynx-server -version
lynx-client -version
sudo systemctl status lynx-server lynx-cloudflared lynx-client
curl -sS http://127.0.0.1:8080/_lynx/v1/version
```

Config field reference: [configuration.md](configuration.md).
