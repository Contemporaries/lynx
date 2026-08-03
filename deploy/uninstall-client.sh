#!/usr/bin/env bash
# Uninstall Lynx Linux client only (keeps lynx-server / cloudflared if present).
# Compatible with 2.0 single-file client.json and legacy multi-file cert layouts.
set -Eeuo pipefail

die() { printf '错误：%s\n' "$*" >&2; exit 1; }
need_root() { [[ ${EUID:-$(id -u)} -eq 0 ]] || die "请使用 root：sudo $0"; }
confirm() {
  local prompt="$1" def="${2:-N}" answer
  read -r -p "$prompt [${def}]: " answer
  answer="${answer:-$def}"
  [[ "$answer" =~ ^[Yy]$ ]]
}

need_root
[[ "$(uname -s)" == Linux ]] || die "仅支持 Linux"

printf '将卸载本机 Lynx 客户端（systemd 服务、二进制、client.json / 旧版证书文件）。\n'
printf '不会删除 lynx-server、cloudflared 软件包，或 Cloudflare Tunnel 配置。\n'
if [[ -f /etc/lynx/server.json ]]; then
  printf '\n检测到 /etc/lynx/server.json：仅移除客户端相关文件，保留服务端配置与 PKI。\n'
fi
confirm "确定继续卸载客户端？" "N" || exit 0

systemctl disable --now lynx-client 2>/dev/null || true
systemctl disable --now lynxvpn-client 2>/dev/null || true

rm -f /etc/systemd/system/lynx-client.service
rm -f /etc/systemd/system/lynxvpn-client.service
rm -f /usr/local/bin/lynx-client /usr/local/bin/lynxvpn-client
rm -f /usr/local/lib/lynx/lynx-client /usr/local/lib/lynxvpn/lynx-client

# 2.0 single-file config + legacy PEM / workdir leftovers
if [[ -f /etc/lynx/server.json ]]; then
  rm -f /etc/lynx/client.json
  rm -f /etc/lynx/ca.crt /etc/lynx/client.crt /etc/lynx/client.key
  rm -rf /etc/lynx/certs
else
  rm -rf /etc/lynx /etc/lynxvpn
fi
rm -rf /var/lib/lynx/subscribe-cache

systemctl daemon-reload

if ! systemctl list-unit-files 'lynx-server.service' 2>/dev/null | grep -q lynx-server.service \
  && ! [[ -x /usr/local/bin/lynx-server ]]; then
  if confirm "未检测到服务端。是否同时删除系统用户 lynx 与 /var/lib/lynx？" "N"; then
    userdel lynx 2>/dev/null || true
    rm -rf /var/lib/lynx
  fi
fi

printf '✓ Lynx 客户端已卸载。\n'
printf '  配置为单文件 client.json（内联 PEM）；旧版三证书路径也已清理（若存在）。\n'
if [[ -f /etc/lynx/server.json ]]; then
  printf '  服务端目录 /etc/lynx 已保留。\n'
fi
