#!/usr/bin/env bash
set -Eeuo pipefail

[[ ${EUID:-$(id -u)} -eq 0 ]] || { echo "请使用 sudo 运行：sudo ./repair-cloudflare-apt.sh"; exit 1; }
command -v apt-get >/dev/null 2>&1 || { echo "此修复脚本仅适用于 Debian/Ubuntu APT 系统。"; exit 1; }

GREEN='\033[32m'; YELLOW='\033[33m'; RED='\033[31m'; RESET='\033[0m'
info(){ printf '➜ %s\n' "$*"; }
ok(){ printf "${GREEN}✓${RESET} %s\n" "$*"; }
warn(){ printf "${YELLOW}!${RESET} %s\n" "$*"; }
die(){ printf "${RED}错误：${RESET}%s\n" "$*" >&2; exit 1; }

BACKUP_DIR="/var/backups/cloudflare-apt-repair-$(date +%Y%m%d%H%M%S)"
HAD_CLOUDFLARED=0
HAD_WARP=0
install -d -m0700 "$BACKUP_DIR"

info "隔离旧 Cloudflare 软件源……"
shopt -s nullglob
for f in /etc/apt/sources.list.d/*.list /etc/apt/sources.list.d/*.sources \
         /etc/apt/sources.list.d/*.list.lynx-disabled /etc/apt/sources.list.d/*.sources.lynx-disabled; do
  [[ -f "$f" ]] || continue
  grep -Eq 'pkg\.cloudflare\.com/cloudflared|pkg\.cloudflareclient\.com' "$f" || continue
  grep -q 'pkg.cloudflare.com/cloudflared' "$f" && HAD_CLOUDFLARED=1
  grep -q 'pkg.cloudflareclient.com' "$f" && HAD_WARP=1
  cp -a "$f" "$BACKUP_DIR/$(basename "$f")"
  rm -f "$f"
done
shopt -u nullglob

if [[ -f /etc/apt/sources.list ]] && grep -Eq 'pkg\.cloudflare\.com/cloudflared|pkg\.cloudflareclient\.com' /etc/apt/sources.list; then
  cp -a /etc/apt/sources.list "$BACKUP_DIR/sources.list"
  grep -q 'pkg.cloudflare.com/cloudflared' /etc/apt/sources.list && HAD_CLOUDFLARED=1
  grep -q 'pkg.cloudflareclient.com' /etc/apt/sources.list && HAD_WARP=1
  sed -i -E '/pkg\.cloudflare\.com\/cloudflared|pkg\.cloudflareclient\.com/ s|^|# disabled by Cloudflare key repair: |' /etc/apt/sources.list
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y ca-certificates curl gnupg
install -d -m0755 /usr/share/keyrings

info "安装 cloudflared 当前签名密钥……"
tmp="$(mktemp)"
curl -fsSL --retry 3 --connect-timeout 15 https://pkg.cloudflare.com/cloudflare-main.gpg -o "$tmp"
[[ -s "$tmp" ]] || die "cloudflared 密钥下载失败。"
install -m0644 "$tmp" /usr/share/keyrings/cloudflare-main.gpg
rm -f "$tmp"
printf '%s\n' 'deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main' \
  > /etc/apt/sources.list.d/cloudflared.list

if (( HAD_WARP )) || dpkg-query -W -f='${Status}' cloudflare-warp 2>/dev/null | grep -q 'install ok installed'; then
  codename="$(. /etc/os-release && printf '%s' "${VERSION_CODENAME:-}")"
  case "$codename" in
    resolute|noble|jammy|focal|bionic|xenial|trixie|bookworm|bullseye|buster|stretch)
      info "安装 Cloudflare WARP 当前签名密钥……"
      tmp="$(mktemp)"
      curl -fsSL --retry 3 --connect-timeout 15 https://pkg.cloudflareclient.com/pubkey.gpg -o "$tmp"
      gpg --batch --yes --dearmor --output /usr/share/keyrings/cloudflare-warp-archive-keyring.gpg "$tmp"
      rm -f "$tmp"
      printf 'deb [signed-by=/usr/share/keyrings/cloudflare-warp-archive-keyring.gpg] https://pkg.cloudflareclient.com/ %s main\n' "$codename" \
        > /etc/apt/sources.list.d/cloudflare-client.list
      ;;
    *)
      warn "系统代号 $codename 不在 Cloudflare WARP 当前支持列表中，WARP 源保持禁用。"
      ;;
  esac
fi

apt-get update -y
ok "Cloudflare APT 软件源已修复"
echo "备份目录：$BACKUP_DIR"
echo "现在可重新运行：sudo ./install.sh"
