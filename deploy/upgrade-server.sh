#!/usr/bin/env bash
# One-click upgrade for Lynx server (Linux amd64/arm64).
set -Eeuo pipefail

REPO="${LYNX_REPO:-Contemporaries/lynx}"
TAG="${1:-latest}"
INSTALL_BIN="${LYNX_SERVER_BIN:-/usr/local/bin/lynx-server}"
RUNTIME_BIN="${LYNX_RUNTIME_SERVER:-/usr/local/lib/lynx/lynx-server}"
BACKUP_DIR="${LYNX_BACKUP_DIR:-/var/backups/lynx}"

die() { printf '错误：%s\n' "$*" >&2; exit 1; }
need_root() { [[ ${EUID:-$(id -u)} -eq 0 ]] || die "请使用 root：sudo $0"; }

arch_suffix() {
  case "$(uname -m)" in
    x86_64|amd64) printf 'linux-amd64' ;;
    aarch64|arm64) printf 'linux-arm64' ;;
    *) die "不支持的架构：$(uname -m)" ;;
  esac
}

api_release() {
  local tag="$1"
  if [[ "$tag" == "latest" ]]; then
    curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest"
  else
    curl -fsSL "https://api.github.com/repos/${REPO}/releases/tags/${tag}"
  fi
}

need_root
command -v curl >/dev/null || die "需要 curl"
command -v sha256sum >/dev/null || die "需要 sha256sum"
command -v systemctl >/dev/null || die "需要 systemctl"

arch="$(arch_suffix)"
asset="lynx-server-${arch}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

printf '➜ 查询 Release %s …\n' "$TAG"
json="$(api_release "$TAG")"
tag_name="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["tag_name"])' <<<"$json")"
url="$(python3 -c 'import json,sys; d=json.load(sys.stdin); 
assets=d.get("assets") or [];
u=next((a["browser_download_url"] for a in assets if a["name"]==sys.argv[1]), "");
print(u)' "$asset" <<<"$json")"
sums_url="$(python3 -c 'import json,sys; d=json.load(sys.stdin); 
assets=d.get("assets") or [];
u=next((a["browser_download_url"] for a in assets if a["name"]=="SHA256SUMS"), "");
print(u)' <<<"$json")"
[[ -n "$url" ]] || die "Release ${tag_name} 中没有 ${asset}"
[[ -n "$sums_url" ]] || die "Release ${tag_name} 中没有 SHA256SUMS"

printf '➜ 下载 %s (%s) …\n' "$asset" "$tag_name"
curl -fsSL -o "$tmp/$asset" "$url"
curl -fsSL -o "$tmp/SHA256SUMS" "$sums_url"
( cd "$tmp" && grep -E "  ${asset}\$" SHA256SUMS | sha256sum -c - )

install -d -m0755 "$BACKUP_DIR"
stamp="$(date +%Y%m%d%H%M%S)"
if [[ -x "$INSTALL_BIN" ]]; then
  install -m0755 "$INSTALL_BIN" "$BACKUP_DIR/lynx-server.${stamp}"
  printf '✓ 已备份 %s → %s\n' "$INSTALL_BIN" "$BACKUP_DIR/lynx-server.${stamp}"
fi
if [[ -d /etc/lynx ]]; then
  tar -czf "$BACKUP_DIR/etc-lynx.${stamp}.tar.gz" -C / etc/lynx
  printf '✓ 已备份 /etc/lynx → %s\n' "$BACKUP_DIR/etc-lynx.${stamp}.tar.gz"
fi

systemctl stop lynx-server 2>/dev/null || true
install -m0755 "$tmp/$asset" "$INSTALL_BIN"
if [[ -d "$(dirname "$RUNTIME_BIN")" ]]; then
  install -m0755 "$tmp/$asset" "$RUNTIME_BIN"
fi
systemctl daemon-reload
systemctl enable lynx-server >/dev/null 2>&1 || true
systemctl start lynx-server
sleep 1
systemctl is-active --quiet lynx-server || {
  journalctl -u lynx-server -n 40 --no-pager || true
  die "lynx-server 启动失败"
}
printf '✓ 服务端已升级到 %s（%s）\n' "$tag_name" "$asset"
printf '核对订阅：sudo lynx-wizard --show-subscribe\n'
printf '并确认 nginx 已配置 location ^~ /_lynx/v1/subscribe/ → 127.0.0.1:8080\n'
