# Uninstall

**English** | [中文](uninstall.zh-CN.md)

Remove Lynx **v2.2.2** client-only or full server stack.

## Client only

```bash
sudo ./deploy/uninstall-client.sh
# or from an installed tree / wizard:
sudo lynx-wizard --uninstall-client
```

Stops and removes `lynx-client` unit/binary and typically `client.json`. Does **not** remove `cloudflared`, `lynx-server`, or the Tunnel.

If no server components remain, `/etc/lynx` may be removed when safe — prefer backing up first.

## Full server uninstall

**Back up `/etc/lynx/pki/ca.key` (and any device material you still need) before proceeding.**

```bash
sudo lynx-wizard --uninstall
```

Removes `lynx-server` / `lynx-client` / `lynx-cloudflared` units, Lynx binaries, `/etc/lynx`, and `/usr/local/lib/lynx` as applicable. The **`cloudflared` package** itself is left installed; delete Tunnel/DNS objects in the Cloudflare dashboard if you no longer need them.

### Manual outline

```bash
sudo systemctl disable --now lynx-server lynx-client lynx-cloudflared 2>/dev/null || true
sudo rm -f /etc/systemd/system/lynx-*.service
sudo systemctl daemon-reload
sudo rm -f /usr/local/bin/lynx-server /usr/local/bin/lynx-client /usr/local/sbin/lynx-wizard
sudo rm -rf /etc/lynx /usr/local/lib/lynx
```

Also remove nginx `location` for `/_lynx/v1/subscribe` if you added it.

## Windows client

Delete the exe, `client.json`, and any shortcuts/tasks you created. No installer service is required by the stock CLI.

## Verify

```bash
systemctl status lynx-server lynx-client lynx-cloudflared 2>&1 | head
which lynx-server lynx-client 2>/dev/null
```
