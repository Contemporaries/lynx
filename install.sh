#!/usr/bin/env bash
set -Eeuo pipefail

VERSION="2.2.0"
SELF_PATH="$(readlink -f "${BASH_SOURCE[0]}")"
SOURCE_ROOT="$(cd "$(dirname "$SELF_PATH")" && pwd)"
RUNTIME_ROOT="/usr/local/lib/lynx"
INSTALL_ROOT="/etc/lynx"
PKI_ROOT="$INSTALL_ROOT/pki"
CERT_ROOT="$INSTALL_ROOT/certs"
STATE_FILE="$INSTALL_ROOT/wizard.env"
SERVER_CONFIG="$INSTALL_ROOT/server.json"
CLIENT_CONFIG="$INSTALL_ROOT/client.json"
WS_PATH="/_lynx/v1/connect"
INNER_NAME="lynx.internal"
DIRECT_PORT="8443"

C_RESET='\033[0m'
C_BOLD='\033[1m'
C_GREEN='\033[32m'
C_YELLOW='\033[33m'
C_RED='\033[31m'
C_CYAN='\033[36m'

info() { printf "${C_CYAN}➜${C_RESET} %s\n" "$*"; }
ok() { printf "${C_GREEN}✓${C_RESET} %s\n" "$*"; }
warn() { printf "${C_YELLOW}!${C_RESET} %s\n" "$*"; }
die() { printf "${C_RED}错误：${C_RESET}%s\n" "$*" >&2; exit 1; }

trap 'printf "\n${C_RED}安装在第 %s 行失败。${C_RESET}\n" "$LINENO" >&2' ERR

banner() {
  clear 2>/dev/null || true
  cat <<EOF
${C_BOLD}Lynx 一键部署向导${C_RESET}  v${VERSION}
Cloudflare CDN + WSS + 内层 TLS 1.3 + mTLS + SOCKS5/HTTP

EOF
}

need_root() {
  [[ ${EUID:-$(id -u)} -eq 0 ]] || die "请使用 sudo 运行：sudo ./lynx-wizard.sh"
  [[ "$(uname -s)" == "Linux" ]] || die "当前向导仅支持 Linux。"
}

ask() {
  local prompt="$1" default="${2:-}" result
  if [[ -n "$default" ]]; then
    read -r -p "$prompt [$default]: " result
    printf '%s' "${result:-$default}"
  else
    read -r -p "$prompt: " result
    printf '%s' "$result"
  fi
}

ask_secret() {
  local prompt="$1" result
  read -r -s -p "$prompt: " result
  printf '\n' >&2
  printf '%s' "$result"
}

confirm() {
  local prompt="$1" default="${2:-Y}" answer suffix
  if [[ "$default" =~ ^[Yy]$ ]]; then suffix="[Y/n]"; else suffix="[y/N]"; fi
  read -r -p "$prompt $suffix: " answer
  answer="${answer:-$default}"
  [[ "$answer" =~ ^[Yy]$ ]]
}

validate_hostname() {
  local v="$1"
  [[ "$v" =~ ^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$ ]]
}

# host, host:port, IPv4, IPv4:port (optional https:// prefix stripped by caller)
validate_subscribe_host() {
  local v="$1" host port
  v="${v#https://}"
  v="${v#http://}"
  v="${v%/}"
  if [[ "$v" == *:* ]]; then
    host="${v%:*}"
    port="${v##*:}"
    [[ "$port" =~ ^[0-9]+$ ]] && ((port >= 1 && port <= 65535)) || return 1
  else
    host="$v"
  fi
  if [[ "$host" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
    return 0
  fi
  validate_hostname "$host"
}

normalize_subscribe_host() {
  local v="$1"
  v="${v#https://}"
  v="${v#http://}"
  v="${v%/}"
  printf '%s' "$v"
}

validate_device() {
  local v="$1"
  [[ "$v" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$ ]]
}

find_root() {
  if [[ -x "$SOURCE_ROOT/dist/lynx-server" && -x "$SOURCE_ROOT/dist/lynx-client" ]]; then
    printf '%s' "$SOURCE_ROOT"
  elif [[ -x "$RUNTIME_ROOT/lynx-server" && -x "$RUNTIME_ROOT/lynx-client" ]]; then
    printf '%s' "$RUNTIME_ROOT"
  else
    die "找不到 Lynx 二进制文件。请从完整安装包中运行此向导。"
  fi
}

check_arch() {
  local arch
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) ;;
    *) die "当前安装包仅包含 Linux AMD64 二进制；检测到架构：$arch" ;;
  esac
}

CF_HAD_CLOUDFLARED_REPO=0
CF_HAD_WARP_REPO=0
CF_APT_BACKUP_DIR=""

prepare_cloudflare_apt_sources() {
  command -v apt-get >/dev/null 2>&1 || return 0
  CF_APT_BACKUP_DIR="/var/backups/lynx-cloudflare-apt-$(date +%Y%m%d%H%M%S)"
  install -d -m0700 "$CF_APT_BACKUP_DIR"

  local f base
  shopt -s nullglob
  for f in /etc/apt/sources.list.d/*.list /etc/apt/sources.list.d/*.sources \
           /etc/apt/sources.list.d/*.list.lynx-disabled /etc/apt/sources.list.d/*.sources.lynx-disabled; do
    [[ -f "$f" ]] || continue
    grep -Eq 'pkg\.cloudflare\.com/cloudflared|pkg\.cloudflareclient\.com' "$f" || continue
    grep -q 'pkg.cloudflare.com/cloudflared' "$f" && CF_HAD_CLOUDFLARED_REPO=1
    grep -q 'pkg.cloudflareclient.com' "$f" && CF_HAD_WARP_REPO=1
    base="$(basename "$f")"
    cp -a "$f" "$CF_APT_BACKUP_DIR/$base"
    rm -f "$f"
  done
  shopt -u nullglob

  if [[ -f /etc/apt/sources.list ]] && grep -Eq 'pkg\.cloudflare\.com/cloudflared|pkg\.cloudflareclient\.com' /etc/apt/sources.list; then
    cp -a /etc/apt/sources.list "$CF_APT_BACKUP_DIR/sources.list"
    grep -q 'pkg.cloudflare.com/cloudflared' /etc/apt/sources.list && CF_HAD_CLOUDFLARED_REPO=1
    grep -q 'pkg.cloudflareclient.com' /etc/apt/sources.list && CF_HAD_WARP_REPO=1
    sed -i -E '/pkg\.cloudflare\.com\/cloudflared|pkg\.cloudflareclient\.com/ s|^|# disabled by Lynx: |' /etc/apt/sources.list
  fi

  if (( CF_HAD_CLOUDFLARED_REPO || CF_HAD_WARP_REPO )); then
    warn "检测到旧 Cloudflare APT 源，已临时隔离并备份到：$CF_APT_BACKUP_DIR"
  fi
}

restore_cloudflare_apt_sources() {
  command -v apt-get >/dev/null 2>&1 || return 0
  install -d -m0755 /usr/share/keyrings

  # Lynx 服务端需要 cloudflared，因此始终按 Cloudflare 当前官方方式建立该源。
  local tmp_key
  tmp_key="$(mktemp)"
  curl -fsSL --retry 3 --connect-timeout 15 https://pkg.cloudflare.com/cloudflare-main.gpg -o "$tmp_key"
  [[ -s "$tmp_key" ]] || die "下载 Cloudflare cloudflared 签名密钥失败。"
  install -m0644 "$tmp_key" /usr/share/keyrings/cloudflare-main.gpg
  rm -f "$tmp_key"
  printf '%s\n' 'deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main' \
    > /etc/apt/sources.list.d/cloudflared.list

  # 仅当系统原来配置过 WARP 源或已安装 WARP 时，更新其轮换后的密钥。
  if (( CF_HAD_WARP_REPO )) || dpkg-query -W -f='${Status}' cloudflare-warp 2>/dev/null | grep -q 'install ok installed'; then
    local codename warp_key
    codename="$(. /etc/os-release && printf '%s' "${VERSION_CODENAME:-}")"
    case "$codename" in
      resolute|noble|jammy|focal|bionic|xenial|trixie|bookworm|bullseye|buster|stretch)
        warp_key="$(mktemp)"
        curl -fsSL --retry 3 --connect-timeout 15 https://pkg.cloudflareclient.com/pubkey.gpg -o "$warp_key"
        gpg --batch --yes --dearmor --output /usr/share/keyrings/cloudflare-warp-archive-keyring.gpg "$warp_key"
        rm -f "$warp_key"
        printf 'deb [signed-by=/usr/share/keyrings/cloudflare-warp-archive-keyring.gpg] https://pkg.cloudflareclient.com/ %s main\n' "$codename" \
          > /etc/apt/sources.list.d/cloudflare-client.list
        ok "Cloudflare WARP 软件源密钥已更新"
        ;;
      *)
        warn "当前系统代号 $codename 不在 Cloudflare WARP 官方支持列表中，旧 WARP 源保持禁用。"
        ;;
    esac
  fi

  apt-get update -y
  ok "Cloudflare APT 软件源已修复"
}

install_base_packages() {
  info "安装系统依赖……"
  if command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    prepare_cloudflare_apt_sources
    apt-get update -y
    apt-get install -y ca-certificates curl gnupg openssl python3 tar gzip coreutils
    restore_cloudflare_apt_sources
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y ca-certificates curl gnupg2 openssl python3 tar gzip coreutils
  elif command -v yum >/dev/null 2>&1; then
    yum install -y ca-certificates curl gnupg2 openssl python3 tar gzip coreutils
  else
    die "暂不支持当前发行版的包管理器。请使用 Debian、Ubuntu、RHEL、Rocky 或 AlmaLinux。"
  fi
  ok "系统依赖已准备；代理模式不需要 TUN、NAT 或修改系统路由"
}

install_cloudflared() {
  if command -v cloudflared >/dev/null 2>&1; then
    ok "cloudflared 已安装：$(cloudflared --version 2>/dev/null | head -n1)"
    return
  fi
  info "从 Cloudflare 官方仓库安装 cloudflared……"
  if command -v apt-get >/dev/null 2>&1; then
    install -d -m0755 /usr/share/keyrings
    curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg \
      | tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null
    printf '%s\n' 'deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main' \
      > /etc/apt/sources.list.d/cloudflared.list
    apt-get update -y
    apt-get install -y cloudflared
  elif command -v dnf >/dev/null 2>&1; then
    curl -fsSL https://pkg.cloudflare.com/cloudflared.repo > /etc/yum.repos.d/cloudflared.repo
    dnf install -y cloudflared
  elif command -v yum >/dev/null 2>&1; then
    curl -fsSL https://pkg.cloudflare.com/cloudflared.repo > /etc/yum.repos.d/cloudflared.repo
    yum install -y cloudflared
  else
    die "无法自动安装 cloudflared。"
  fi
  ok "cloudflared 安装完成"
}

ensure_lynx_user() {
  if ! id -u lynx >/dev/null 2>&1; then
    useradd --system --home /var/lib/lynx --shell /usr/sbin/nologin --create-home lynx
  fi
  install -d -m0750 -o lynx -g lynx /var/lib/lynx
  install -d -m0750 -o lynx -g lynx "$INSTALL_ROOT"
  install -d -m0750 -o lynx -g lynx "$CERT_ROOT"
}

install_runtime_files() {
  local root="$1"
  ensure_lynx_user
  install -d -m0755 "$RUNTIME_ROOT"
  install -m0755 "$root/dist/lynx-server" "$RUNTIME_ROOT/lynx-server"
  install -m0755 "$root/dist/lynx-client" "$RUNTIME_ROOT/lynx-client"
  install -m0644 "$root/deploy/lynx-server.service" "$RUNTIME_ROOT/lynx-server.service"
  install -m0644 "$root/deploy/lynx-client.service" "$RUNTIME_ROOT/lynx-client.service"
  install -m0755 "$root/deploy/upgrade-server.sh" "$RUNTIME_ROOT/upgrade-server.sh"
  install -m0755 "$root/deploy/upgrade-client.sh" "$RUNTIME_ROOT/upgrade-client.sh"
  install -m0755 "$root/deploy/uninstall-client.sh" "$RUNTIME_ROOT/uninstall-client.sh"
  install -m0644 "$root/deploy/nginx-subscribe.conf.example" "$RUNTIME_ROOT/nginx-subscribe.conf.example"
  install -m0755 "$SELF_PATH" /usr/local/sbin/lynx-wizard
}

generate_pki() {
  local direct_name="$1" device="$2"
  install -d -m0700 "$PKI_ROOT" "$CERT_ROOT"
  umask 077

  if [[ ! -f "$PKI_ROOT/ca.key" ]]; then
    info "生成私人 CA……"
    openssl genpkey -algorithm ED25519 -out "$PKI_ROOT/ca.key"
    openssl req -x509 -new -key "$PKI_ROOT/ca.key" -out "$PKI_ROOT/ca.crt" \
      -days 3650 -sha256 -subj "/CN=Lynx Private CA"
  fi

  info "生成服务端证书……"
  openssl genpkey -algorithm ED25519 -out "$PKI_ROOT/server.key"
  openssl req -new -key "$PKI_ROOT/server.key" -out "$PKI_ROOT/server.csr" -subj "/CN=$INNER_NAME"
  cat > "$PKI_ROOT/server.ext" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=serverAuth
subjectAltName=DNS:${direct_name},DNS:${INNER_NAME}
EOF
  openssl x509 -req -in "$PKI_ROOT/server.csr" -CA "$PKI_ROOT/ca.crt" -CAkey "$PKI_ROOT/ca.key" \
    -CAcreateserial -out "$PKI_ROOT/server.crt" -days 825 -sha256 -extfile "$PKI_ROOT/server.ext"
  rm -f "$PKI_ROOT/server.csr" "$PKI_ROOT/server.ext"

  install -m0644 "$PKI_ROOT/ca.crt" "$CERT_ROOT/ca.crt"
  install -m0644 "$PKI_ROOT/server.crt" "$CERT_ROOT/server.crt"
  install -m0600 "$PKI_ROOT/server.key" "$CERT_ROOT/server.key"
  # Runtime certs are owned by lynx; CA private key stays only under PKI_ROOT for admin use.
  chown -R lynx:lynx "$CERT_ROOT"
  chmod 0600 "$PKI_ROOT/ca.key"
  warn "请将 $PKI_ROOT/ca.key 备份到离线介质；日常运行不需要它。"
  generate_client_cert "$device"
}

client_cert_fingerprint() {
  local cert="$1"
  openssl x509 -in "$cert" -noout -fingerprint -sha256 | sed 's/^.*=//' | tr -d ':' | tr 'A-F' 'a-f'
}

generate_client_cert() {
  local device="$1"
  validate_device "$device" || die "设备名称只能包含字母、数字、点、下划线和短横线。"
  [[ -f "$PKI_ROOT/ca.key" && -f "$PKI_ROOT/ca.crt" ]] || die "私人 CA 不存在。请在管理机持有 ca.key 后签发，或先完成服务端 PKI 初始化。"
  umask 077
  openssl genpkey -algorithm ED25519 -out "$PKI_ROOT/${device}.key"
  openssl req -new -key "$PKI_ROOT/${device}.key" -out "$PKI_ROOT/${device}.csr" -subj "/CN=${device}"
  cat > "$PKI_ROOT/client.ext" <<'EOF'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=clientAuth
EOF
  openssl x509 -req -in "$PKI_ROOT/${device}.csr" -CA "$PKI_ROOT/ca.crt" -CAkey "$PKI_ROOT/ca.key" \
    -CAcreateserial -out "$PKI_ROOT/${device}.crt" -days 825 -sha256 -extfile "$PKI_ROOT/client.ext"
  rm -f "$PKI_ROOT/${device}.csr" "$PKI_ROOT/client.ext"
  chmod 0600 "$PKI_ROOT/${device}.key"
  chmod 0644 "$PKI_ROOT/${device}.crt"
  # lynx user must read device certs to serve subscriptions; keep ca.key root-only.
  chown root:lynx "$PKI_ROOT" 2>/dev/null || true
  chmod 0750 "$PKI_ROOT" 2>/dev/null || true
  chown root:lynx "$PKI_ROOT/${device}.crt" "$PKI_ROOT/${device}.key"
  chmod 0640 "$PKI_ROOT/${device}.key"
  ok "已生成设备证书：$device（指纹 $(client_cert_fingerprint "$PKI_ROOT/${device}.crt")）"
}

write_server_config() {
  local direct_listen="$1" first_device="$2" fingerprint="$3" cdn_host="$4" subscribe_host="$5" token="$6"
  python3 - "$SERVER_CONFIG" "$direct_listen" "$first_device" "$fingerprint" "$cdn_host" "$subscribe_host" "$token" <<'PY'
import json, sys
path, direct, device, fp, cdn, sub_host, token = sys.argv[1:]
data = {
  "direct_listen": direct,
  "ws_listen": "127.0.0.1:8080",
  "ws_path": "/_lynx/v1/connect",
  "public_base_url": f"https://{sub_host}",
  "cdn_base_url": f"https://{cdn}",
  "subscribe_path_prefix": "/_lynx/v1/subscribe/",
  "cert_file": "/etc/lynx/certs/server.crt",
  "key_file": "/etc/lynx/certs/server.key",
  "client_ca_file": "/etc/lynx/certs/ca.crt",
  "clients": {
    device: {
      "certificate_sha256": fp,
      "enabled": True,
      "subscribe_token": token,
      "cert_file": f"/etc/lynx/pki/{device}.crt",
      "key_file": f"/etc/lynx/pki/{device}.key",
    }
  },
  "allow_private_networks": False,
  "max_proxy_flows_per_session": 256,
  "proxy_dial_timeout_seconds": 15,
  "security": {
    "max_sessions_per_certificate": 4,
    "max_sessions_per_source_ip": 8,
    "max_total_sessions": 256,
    "max_flows_per_certificate": 512,
    "max_new_flows_per_second": 50,
    "handshake_timeout_seconds": 10,
    "session_idle_timeout_seconds": 300,
    "flow_idle_timeout_seconds": 600,
    "max_subscribe_per_ip_per_min": 30,
    "max_subscribe_per_token_per_min": 10
  }
}
with open(path, "w") as f:
    json.dump(data, f, indent=2)
    f.write("\n")
PY
  chmod 0600 "$SERVER_CONFIG"
  chown lynx:lynx "$SERVER_CONFIG"
}

gen_subscribe_token() {
  openssl rand -hex 32
}

subscribe_url_for() {
  local token="$1"
  # shellcheck disable=SC1090
  source "$STATE_FILE" 2>/dev/null || true
  local host="${SUBSCRIBE_HOST:-}"
  if [[ -z "$host" && -f "$SERVER_CONFIG" ]]; then
    host="$(python3 - "$SERVER_CONFIG" <<'PY'
import json,sys
with open(sys.argv[1]) as f:d=json.load(f)
print(d.get('public_base_url','').removeprefix('https://').removeprefix('http://'))
PY
)"
  fi
  [[ -n "$host" ]] || host="${CDN_HOST:-}"
  printf 'https://%s/_lynx/v1/subscribe/%s' "$host" "$token"
}

# Tunnel only needs /_lynx/v1/connect; subscribe goes through user's nginx on the direct domain.
restore_tunnel_connect_only() {
  local yml="$INSTALL_ROOT/cloudflared.yml"
  [[ -f "$yml" ]] || { warn "未找到 $yml，跳过 Tunnel 调整。"; return 0; }
  # shellcheck disable=SC1090
  source "$STATE_FILE" 2>/dev/null || true
  python3 - "$yml" "$WS_PATH" "${CDN_HOST:-}" <<'PY'
import pathlib, re, sys
path = pathlib.Path(sys.argv[1])
ws_path = sys.argv[2]
cdn = sys.argv[3].strip()
text = path.read_text()
m_tid = re.search(r'(?m)^tunnel:\s*(\S+)', text)
m_cred = re.search(r'(?m)^credentials-file:\s*(\S+)', text)
if not m_tid or not m_cred:
    raise SystemExit('cannot parse tunnel id / credentials-file')
host = cdn
if not host:
    m_host = re.search(r'(?m)^[ \t]*hostname:\s*(\S+)', text)
    host = m_host.group(1) if m_host else ''
if not host:
    raise SystemExit('CDN hostname unknown; set CDN_HOST in wizard.env')
new = (
    f"tunnel: {m_tid.group(1)}\n"
    f"credentials-file: {m_cred.group(1)}\n"
    f"protocol: quic\n"
    f"ingress:\n"
    f"  - hostname: {host}\n"
    f"    path: ^{ws_path}$\n"
    f"    service: http://127.0.0.1:8080\n"
    f"  - service: http_status:404\n"
)
path.write_text(new)
print('updated')
PY
  [[ $? -eq 0 ]] || die "无法调整 $yml"
  chmod 0600 "$yml"
  if systemctl is-active --quiet lynx-cloudflared 2>/dev/null; then
    systemctl restart lynx-cloudflared
    sleep 2
    systemctl is-active --quiet lynx-cloudflared || {
      journalctl -u lynx-cloudflared -n 40 --no-pager || true
      die "重启 lynx-cloudflared 失败"
    }
  fi
  ok "Cloudflare Tunnel 已限制为仅 ${WS_PATH}（订阅走 nginx 直链）"
}

# Compatibility alias
fix_cloudflare_ingress() {
  restore_tunnel_connect_only
}

print_nginx_subscribe_hint() {
  local hostport="${1:-}"
  local host port=""
  hostport="$(normalize_subscribe_host "$hostport")"
  if [[ "$hostport" == *:* ]]; then
    host="${hostport%:*}"
    port="${hostport##*:}"
  else
    host="$hostport"
  fi
  printf '\n%s\n' "${C_BOLD}nginx 订阅反代（二选一）：${C_RESET}"
  if [[ -n "$port" && "$port" != "443" ]]; then
    cat <<EOF
# A) 独立端口 ${port}（推荐与主站业务分离）
server {
    listen ${port} ssl;
    server_name ${host:-_};
    ssl_certificate     /root/cert/fullchain.pem;
    ssl_certificate_key /root/cert/privkey.pem;

    location ^~ /_lynx/v1/subscribe {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header CF-Connecting-IP \$remote_addr;
        proxy_buffering off;
        add_header Cache-Control "no-store" always;
    }
}
# 安全组放行 TCP ${port}；public_base_url=https://${host}:${port}
EOF
  else
    cat <<EOF
# A) 独立 server_name（与主站业务分离）
server {
    listen 443 ssl;
    server_name ${host:-subscribe.example.com};
    ssl_certificate     /root/cert/fullchain.pem;
    ssl_certificate_key /root/cert/privkey.pem;

    location ^~ /_lynx/v1/subscribe {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header CF-Connecting-IP \$remote_addr;
        proxy_buffering off;
        add_header Cache-Control "no-store" always;
    }
}

# B) 或嵌入现有 443 server，且必须写在 location / 之前：
# location ^~ /_lynx/v1/subscribe/ { proxy_pass http://127.0.0.1:8080; ... }
EOF
  fi
  printf '完整示例：deploy/nginx-subscribe.conf.example\n'
  printf '然后：nginx -t && systemctl reload nginx\n'
  printf '本机探测：curl -sS http://127.0.0.1:8080/_lynx/v1/subscribe\n'
}

show_subscribe() {
  banner
  need_root
  [[ -f "$SERVER_CONFIG" ]] || die "未找到 $SERVER_CONFIG"
  info "二进制版本…"
  if [[ -x /usr/local/bin/lynx-server ]]; then
    /usr/local/bin/lynx-server -version 2>/dev/null || warn "当前 lynx-server 无 -version，请安装 v2.2.0+ 二进制"
  fi
  info "本机 origin 探测…"
  local verbody health
  verbody="$(curl -sS --max-time 3 http://127.0.0.1:8080/_lynx/v1/version || true)"
  health="$(curl -sS --max-time 3 http://127.0.0.1:8080/_lynx/v1/subscribe || true)"
  if [[ -z "$health" && -z "$verbody" ]]; then
    warn "无法连接 127.0.0.1:8080 —— 请确认 lynx-server 已启动"
  fi
  [[ -n "$verbody" ]] && printf 'version: %s\n' "$verbody"
  [[ -n "$health" ]] && printf 'health: %s\n' "$health"
  if [[ "$health" == *"404 page not found"* ]] || [[ "$verbody" == *"404 page not found"* ]]; then
    warn "8080 上没有订阅路由：正在运行的是不含 subscribe 的旧二进制。"
    warn "请执行：sudo ./deploy/upgrade-server.sh v2.2.0 && sudo systemctl restart lynx-server"
    warn "然后再次：curl -sS http://127.0.0.1:8080/_lynx/v1/version"
  fi
  # Port conflict hint
  python3 - "$SERVER_CONFIG" <<'PY' || true
import json,sys,re
with open(sys.argv[1]) as f:d=json.load(f)
pub=(d.get("public_base_url") or "")
direct=d.get("direct_listen") or ""
m=re.search(r":(\d+)$", pub.replace("https://","").replace("http://",""))
dm=re.search(r":(\d+)$", direct)
if m and dm and m.group(1)==dm.group(1):
    print(f"! 冲突：public_base_url 端口 {m.group(1)} 与 direct_listen {direct} 相同（mTLS）。请把订阅改为 https://域名（443/nginx path）并配置 nginx，直连占用 :8443。", file=sys.stderr)
PY
  info "已配置的订阅 URL："
  python3 - "$SERVER_CONFIG" <<'PY'
import json, sys, urllib.request
path = sys.argv[1]
with open(path) as f:
    data = json.load(f)
base = (data.get("public_base_url") or "").rstrip("/")
prefix = data.get("subscribe_path_prefix") or "/_lynx/v1/subscribe/"
if not prefix.endswith("/"):
    prefix += "/"
clients = data.get("clients") or {}
n = 0
for name, entry in clients.items():
    if not isinstance(entry, dict):
        continue
    tok = (entry.get("subscribe_token") or "").strip()
    if not tok:
        print(f"{name}\t(MISSING subscribe_token)")
        continue
    n += 1
    print(f"{name}\t{base}{prefix}{tok}")
if n == 0:
    print("没有可用的 subscribe_token。请在 server.json 的 clients 中补齐，或重新添加设备。", file=sys.stderr)
    raise SystemExit(2)
PY
  local first
  first="$(python3 - "$SERVER_CONFIG" <<'PY'
import json,sys
with open(sys.argv[1]) as f:d=json.load(f)
for e in (d.get("clients") or {}).values():
    if isinstance(e,dict) and e.get("subscribe_token"):
        print(e["subscribe_token"].strip()); break
PY
)"
  if [[ -n "$first" ]]; then
    local code
    code="$(curl -sS -o /tmp/lynx-sub-body.json -w '%{http_code}' --max-time 5 "http://127.0.0.1:8080/_lynx/v1/subscribe/${first}" || true)"
    printf '本机拉订阅 HTTP %s\n' "$code"
    if [[ "$code" == "200" ]]; then
      ok "本机 origin 订阅可用"
    elif [[ "$code" == "404" ]]; then
      warn "本机返回 404：token 未加载或二进制过旧。查看：journalctl -u lynx-server -n 50"
      warn "并确认 server.json 里 clients.*.subscribe_token 非空后重启 lynx-server"
    else
      warn "本机拉订阅异常，响应头/体见 /tmp/lynx-sub-body.json"
      head -c 300 /tmp/lynx-sub-body.json 2>/dev/null; echo
    fi
  fi
  # shellcheck disable=SC1090
  source "$STATE_FILE" 2>/dev/null || true
  print_nginx_subscribe_hint "${SUBSCRIBE_HOST:-}"
}

install_server_service() {
  ensure_lynx_user
  chown -R lynx:lynx "$CERT_ROOT" "$INSTALL_ROOT"
  chmod 0600 "$CERT_ROOT/server.key" 2>/dev/null || true
  install -m0755 "$RUNTIME_ROOT/lynx-server" /usr/local/bin/lynx-server
  install -m0644 "$RUNTIME_ROOT/lynx-server.service" /etc/systemd/system/lynx-server.service
  systemctl daemon-reload
  systemctl enable lynx-server >/dev/null
  systemctl restart lynx-server
  sleep 1
  systemctl is-active --quiet lynx-server || {
    journalctl -u lynx-server -n 50 --no-pager || true
    die "Lynx 服务端启动失败。"
  }
  ok "Lynx 代理服务端已启动"
}

setup_cloudflare_tunnel() {
  local cdn_host="$1" tunnel_name="$2"
  install_cloudflared
  install -d -m0700 /root/.cloudflared "$INSTALL_ROOT/cloudflared"

  if [[ ! -f /root/.cloudflared/cert.pem ]]; then
    printf '\n${C_BOLD}接下来 Cloudflare 会显示一个登录网址。${C_RESET}\n'
    printf '请在浏览器打开该网址，登录 Cloudflare，并选择 %s 所属域名。\n\n' "$cdn_host"
    cloudflared tunnel login
  else
    ok "检测到现有 Cloudflare 登录凭据"
  fi

  local create_out tunnel_id cred_src
  info "创建 Cloudflare Tunnel：$tunnel_name"
  create_out="$(cloudflared tunnel create "$tunnel_name" 2>&1)" || {
    printf '%s\n' "$create_out" >&2
    die "创建 Cloudflare Tunnel 失败。若名称已存在，请重新运行并使用其他名称。"
  }
  printf '%s\n' "$create_out"
  tunnel_id="$(grep -Eo '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}' <<<"$create_out" | tail -n1)"
  [[ -n "$tunnel_id" ]] || die "无法从 cloudflared 输出中识别 Tunnel UUID。"
  cred_src="/root/.cloudflared/${tunnel_id}.json"
  [[ -f "$cred_src" ]] || die "未找到 Tunnel 凭据文件：$cred_src"
  install -m0600 "$cred_src" "$INSTALL_ROOT/cloudflared/${tunnel_id}.json"

  info "把 $cdn_host 指向新 Tunnel（会尝试覆盖同名 DNS 记录）……"
  if ! cloudflared tunnel route dns --overwrite-dns "$tunnel_id" "$cdn_host"; then
    warn "自动更新 DNS 失败。请在 Cloudflare DNS 中删除 $cdn_host 的旧 A/AAAA/CNAME 记录后，运行："
    printf '  sudo cloudflared tunnel route dns %s %s\n' "$tunnel_id" "$cdn_host"
    read -r -p "完成后按 Enter 继续，或按 Ctrl+C 退出……"
    cloudflared tunnel route dns "$tunnel_id" "$cdn_host"
  fi

  cat > "$INSTALL_ROOT/cloudflared.yml" <<EOF
tunnel: ${tunnel_id}
credentials-file: ${INSTALL_ROOT}/cloudflared/${tunnel_id}.json
protocol: quic
ingress:
  - hostname: ${cdn_host}
    path: ^${WS_PATH}$
    service: http://127.0.0.1:8080
  - service: http_status:404
EOF
  chmod 0600 "$INSTALL_ROOT/cloudflared.yml"

  cat > /etc/systemd/system/lynx-cloudflared.service <<'EOF'
[Unit]
Description=Cloudflare Tunnel for Lynx
After=network-online.target lynx-server.service
Wants=network-online.target
Requires=lynx-server.service

[Service]
Type=simple
ExecStart=/usr/bin/cloudflared --no-autoupdate --config /etc/lynx/cloudflared.yml tunnel run
Restart=always
RestartSec=5
# cloudflared needs Cloudflare login credentials under /root/.cloudflared
User=root
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadOnlyPaths=/etc/lynx

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable --now lynx-cloudflared >/dev/null
  sleep 2
  systemctl is-active --quiet lynx-cloudflared || {
    journalctl -u lynx-cloudflared -n 80 --no-pager || true
    die "Cloudflare Tunnel 服务启动失败。"
  }
  ok "Cloudflare Tunnel 已连接"
  SETUP_TUNNEL_ID="$tunnel_id"
}

save_state() {
  local cdn_host="$1" direct_enabled="$2" direct_host="$3" direct_port="$4" tunnel_id="$5" subscribe_host="${6:-}"
  [[ -n "$subscribe_host" ]] || subscribe_host="$cdn_host"
  umask 077
  cat > "$STATE_FILE" <<EOF
CDN_HOST=$(printf '%q' "$cdn_host")
SUBSCRIBE_HOST=$(printf '%q' "$subscribe_host")
DIRECT_ENABLED=$(printf '%q' "$direct_enabled")
DIRECT_HOST=$(printf '%q' "$direct_host")
DIRECT_PORT=$(printf '%q' "$direct_port")
INNER_NAME=$(printf '%q' "$INNER_NAME")
WS_PATH=$(printf '%q' "$WS_PATH")
TUNNEL_ID=$(printf '%q' "$tunnel_id")
EOF
  chmod 0600 "$STATE_FILE"
}

add_client_mapping() {
  local device="$1" fingerprint="$2" token="$3"
  python3 - "$SERVER_CONFIG" "$device" "$fingerprint" "$token" <<'PY'
import json, sys
path, device, fp, token = sys.argv[1:]
with open(path) as f:
    data=json.load(f)
clients=data.setdefault('clients', {})
for k,v in list(clients.items()):
    if isinstance(v, str):
        del clients[k]
entry=clients.get(device) if isinstance(clients.get(device), dict) else {}
if not token:
    token=entry.get('subscribe_token') or ''
if not token:
    import secrets
    token=secrets.token_hex(32)
clients[device]={
  "certificate_sha256": fp,
  "enabled": True,
  "subscribe_token": token,
  "cert_file": f"/etc/lynx/pki/{device}.crt",
  "key_file": f"/etc/lynx/pki/{device}.key",
}
data.setdefault('subscribe_path_prefix', '/_lynx/v1/subscribe/')
data.setdefault('cdn_base_url', data.get('cdn_base_url') or data.get('public_base_url') or '')
data.setdefault('security', {
  "max_sessions_per_certificate": 4,
  "max_sessions_per_source_ip": 8,
  "max_total_sessions": 256,
  "max_flows_per_certificate": 512,
  "max_new_flows_per_second": 50,
  "handshake_timeout_seconds": 10,
  "session_idle_timeout_seconds": 300,
  "flow_idle_timeout_seconds": 600,
  "max_subscribe_per_ip_per_min": 30,
  "max_subscribe_per_token_per_min": 10
})
with open(path,'w') as f:
    json.dump(data,f,indent=2)
    f.write('\n')
print(token)
PY
  chmod 0600 "$SERVER_CONFIG"
  chown lynx:lynx "$SERVER_CONFIG"
  systemctl restart lynx-server >/dev/null
}

output_home() {
  local user="${SUDO_USER:-root}" home
  home="$(getent passwd "$user" 2>/dev/null | cut -d: -f6 || true)"
  [[ -n "$home" && -d "$home" ]] || home="/root"
  printf '%s' "$home"
}

make_client_bundle() {
  local device="$1" token="$2" cf_id="${3:-}" cf_secret="${4:-}"
  # shellcheck disable=SC1090
  source "$STATE_FILE"
  local tmp bundle_dir out_dir archive mode direct_addr direct_name sub_url sub_host
  tmp="$(mktemp -d)"
  bundle_dir="$tmp/lynx-client-${device}"
  mkdir -p "$bundle_dir"
  sub_host="${SUBSCRIBE_HOST:-$CDN_HOST}"
  sub_url="https://${sub_host}/_lynx/v1/subscribe/${token}"

  if [[ "${DIRECT_ENABLED:-no}" == "yes" ]]; then
    mode="auto"
    direct_addr="${DIRECT_HOST}:${DIRECT_PORT}"
    direct_name="$DIRECT_HOST"
  else
    mode="wss"
    direct_addr=""
    direct_name=""
  fi

  install -m0755 "$RUNTIME_ROOT/lynx-client" "$bundle_dir/lynx-client"
  install -m0644 "$RUNTIME_ROOT/lynx-client.service" "$bundle_dir/lynx-client.service"
  local uninstall_src="" src_root=""
  if [[ -x "$RUNTIME_ROOT/uninstall-client.sh" ]]; then
    uninstall_src="$RUNTIME_ROOT/uninstall-client.sh"
  else
    src_root="$(find_root 2>/dev/null || true)"
    if [[ -n "$src_root" && -x "$src_root/deploy/uninstall-client.sh" ]]; then
      uninstall_src="$src_root/deploy/uninstall-client.sh"
    fi
  fi
  [[ -n "$uninstall_src" ]] && install -m0755 "$uninstall_src" "$bundle_dir/uninstall-client.sh"

  # Single-file client.json (subscribe_url only; first run fetches inline PEMs into this file).
  python3 - "$bundle_dir/client.json" "$sub_url" "$mode" "$direct_addr" "$direct_name" "$cf_id" "$cf_secret" <<'PY'
import json, sys
path, sub, mode, direct_addr, direct_name, cfid, cfsecret = sys.argv[1:]
data = {
  "subscribe_url": sub,
  "mode": mode,
  "socks_listen": "127.0.0.1:1080",
  "http_listen": "127.0.0.1:8080",
  "proxy_channels": 3,
  "ping_interval_seconds": 20,
  "pong_timeout_misses": 3,
}
if direct_addr:
  data["direct_addr"] = direct_addr
  data["direct_server_name"] = direct_name
if cfid:
  data["cf_access_client_id"] = cfid
  data["cf_access_client_secret"] = cfsecret
with open(path, "w") as f:
  json.dump(data, f, indent=2)
  f.write("\n")
PY
  chmod 0600 "$bundle_dir/client.json"

  cat > "$bundle_dir/install-client.sh" <<'CLIENT_INSTALL'
#!/usr/bin/env bash
set -Eeuo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GREEN='\033[32m'; YELLOW='\033[33m'; RED='\033[31m'; RESET='\033[0m'
[[ ${EUID:-$(id -u)} -eq 0 ]] || { echo "请使用 sudo 运行：sudo ./install-client.sh"; exit 1; }
[[ "$(uname -s)" == Linux ]] || { echo "当前客户端安装包仅支持 Linux。"; exit 1; }
ask() { local prompt="$1" default="$2" value; read -r -p "$prompt [$default]: " value; printf '%s' "${value:-$default}"; }
confirm() { local prompt="$1" answer; read -r -p "$prompt [y/N]: " answer; [[ "${answer:-N}" =~ ^[Yy]$ ]]; }
valid_port() { [[ "$1" =~ ^[0-9]+$ ]] && (( $1 >= 1 && $1 <= 65535 )); }

printf 'Lynx 客户端安装（单文件 client.json）\n\n'
while true; do
  BIND_ADDR="$(ask '本地监听地址' '127.0.0.1')"
  [[ "$BIND_ADDR" =~ ^[A-Za-z0-9.-]+$ ]] && break
done
while true; do
  SOCKS_PORT="$(ask 'SOCKS5 端口，0=关闭' '1080')"
  [[ "$SOCKS_PORT" == 0 ]] || valid_port "$SOCKS_PORT" && break
done
while true; do
  HTTP_PORT="$(ask 'HTTP 端口，0=关闭' '8080')"
  [[ "$HTTP_PORT" == 0 ]] || valid_port "$HTTP_PORT" && break
done
[[ "$SOCKS_PORT" != 0 || "$HTTP_PORT" != 0 ]] || { echo 'SOCKS5 和 HTTP 不能同时关闭。'; exit 1; }
PROXY_USER=''; PROXY_PASS=''
if [[ "$BIND_ADDR" != '127.0.0.1' && "$BIND_ADDR" != '::1' && "$BIND_ADDR" != 'localhost' ]]; then
  NEED_AUTH=yes
elif confirm '是否为本地代理设置用户名和密码？'; then NEED_AUTH=yes; else NEED_AUTH=no; fi
if [[ "$NEED_AUTH" == yes ]]; then
  read -r -p '代理用户名: ' PROXY_USER
  read -r -s -p '代理密码: ' PROXY_PASS; printf '\n'
fi
CHANNELS="$(ask '连接池数量' '3')"
SOCKS_ADDR=''; HTTP_ADDR=''
[[ "$SOCKS_PORT" != 0 ]] && SOCKS_ADDR="${BIND_ADDR}:${SOCKS_PORT}"
[[ "$HTTP_PORT" != 0 ]] && HTTP_ADDR="${BIND_ADDR}:${HTTP_PORT}"
python3 - "$ROOT/client.json" "$SOCKS_ADDR" "$HTTP_ADDR" "$CHANNELS" "$PROXY_USER" "$PROXY_PASS" <<'PY'
import json,sys
path,socks,http,ch,user,pw=sys.argv[1:]
with open(path) as f:d=json.load(f)
d["socks_listen"]=socks
d["http_listen"]=http
d["proxy_channels"]=int(ch)
if user:
  d["proxy_username"]=user
  d["proxy_password"]=pw
else:
  d.pop("proxy_username",None); d.pop("proxy_password",None)
with open(path,"w") as f: json.dump(d,f,indent=2); f.write("\n")
PY
read -r -p '确认安装？[Y/n]: ' ANSWER
[[ "${ANSWER:-Y}" =~ ^[Yy]$ ]] || exit 0
if ! id -u lynx >/dev/null 2>&1; then
  useradd --system --home /var/lib/lynx --shell /usr/sbin/nologin --create-home lynx
fi
install -d -m0750 -o lynx -g lynx /var/lib/lynx
systemctl disable --now lynx-client 2>/dev/null || true
install -d -m0700 /etc/lynx
install -m0755 "$ROOT/lynx-client" /usr/local/bin/lynx-client
install -m0644 "$ROOT/lynx-client.service" /etc/systemd/system/lynx-client.service
install -m0600 "$ROOT/client.json" /etc/lynx/client.json
chown -R lynx:lynx /etc/lynx
systemctl daemon-reload
systemctl enable --now lynx-client
sleep 3
if systemctl is-active --quiet lynx-client; then
  printf "${GREEN}✓ Lynx 已启动（配置：/etc/lynx/client.json）。${RESET}\n"
else
  printf "${RED}启动失败：${RESET}\n"; journalctl -u lynx-client -n 80 --no-pager || true; exit 1
fi
CLIENT_INSTALL
  chmod 0755 "$bundle_dir/install-client.sh"
  cat > "$bundle_dir/使用说明.txt" <<EOF
设备：${device}
只需一份配置 client.json（内含 subscribe_url）。

订阅地址：
  ${sub_url}

Linux：
  sudo ./install-client.sh
  # 或
  lynx-client -subscribe '${sub_url}' -config /etc/lynx/client.json

卸载客户端：
  sudo ./uninstall-client.sh
  # 或仓库内：sudo ./deploy/uninstall-client.sh

首次启动会拉取证书并写回 client.json（certificate / key / certificate_authority）。
订阅链接等同密钥，请勿公开分享。
EOF
  out_dir="$(output_home)"
  archive="$out_dir/lynx-client-${device}.tar.gz"
  tar -C "$tmp" -czf "$archive" "lynx-client-${device}"
  chmod 0600 "$archive"
  if [[ -n "${SUDO_USER:-}" && "$SUDO_USER" != root ]]; then chown "$SUDO_USER":"$(id -gn "$SUDO_USER")" "$archive"; fi
  rm -rf "$tmp"
  printf '%s' "$archive"
}

server_install() {
  banner
  need_root
  check_arch
  local root cdn_host subscribe_host direct_enabled direct_host device cf_access cf_id="" cf_secret="" tunnel_name tunnel_id archive
  root="$(find_root)"

  printf "${C_BOLD}你需要准备：${C_RESET}\n"
  printf '  1. 一个已经托管在 Cloudflare 的 CDN 子域名（代理 WSS）\n'
  printf '  2. 一个已有公网证书的直链域名 + nginx（订阅 HTTPS）\n'
  printf '  3. 可以在浏览器中登录 Cloudflare\n\n'

  while true; do
    cdn_host="$(ask "Cloudflare CDN 域名，例如 cdn.example.com")"
    validate_hostname "$cdn_host" && break
    warn "域名格式不正确，请重新输入。"
  done

  while true; do
    subscribe_host="$(ask "订阅域名（nginx HTTPS，默认 443 path；勿用直连口 ${DIRECT_PORT}），例如 subscribe.example.com")"
    subscribe_host="$(normalize_subscribe_host "$subscribe_host")"
    validate_subscribe_host "$subscribe_host" || { warn "格式不正确（支持 域名、域名:端口、IP、IP:端口）。"; continue; }
    # Guard against colliding with direct mTLS port
    if [[ "$subscribe_host" == *":${DIRECT_PORT}" ]]; then
      warn "订阅不能占用直连端口 ${DIRECT_PORT}，已去掉端口（改走 443/nginx）"
      subscribe_host="${subscribe_host%:*}"
    fi
    break
  done

  direct_enabled="no"
  direct_host="$INNER_NAME"
  if confirm "是否同时启用直连加速？这需要服务器开放 TCP ${DIRECT_PORT}" "N"; then
    direct_enabled="yes"
    while true; do
      direct_default="${subscribe_host%%:*}"
      direct_host="$(ask "直连域名，例如 direct.example.com" "$direct_default")"
      validate_hostname "$direct_host" && break
      warn "域名格式不正确，请重新输入。"
    done
  fi

  while true; do
    device="$(ask "第一台客户端设备名称" "laptop")"
    validate_device "$device" && break
    warn "设备名称格式不正确。"
  done

  cf_access="no"
  if confirm "你的 CDN 域名是否已经启用 Cloudflare Access Service Token？" "N"; then
    cf_access="yes"
    cf_id="$(ask "CF-Access-Client-Id")"
    cf_secret="$(ask_secret "CF-Access-Client-Secret")"
  fi

  tunnel_name="lynx-$(hostname -s | tr -cd 'A-Za-z0-9-')-$(date +%Y%m%d%H%M%S)"
  printf "\n${C_BOLD}即将执行：${C_RESET}\n"
  printf '  CDN 域名：%s\n' "$cdn_host"
  printf '  订阅直链：%s\n' "$subscribe_host"
  printf '  直连加速：%s\n' "$direct_enabled"
  [[ "$direct_enabled" == yes ]] && printf '  直连域名：%s:%s\n' "$direct_host" "$DIRECT_PORT"
  printf '  首台设备：%s\n' "$device"
  printf '  Cloudflare Tunnel：自动创建并写入 DNS\n\n'
  confirm "确认开始部署？" "Y" || exit 0

  install_base_packages
  install_runtime_files "$root"
  install -d -m0700 "$INSTALL_ROOT" "$PKI_ROOT" "$CERT_ROOT"
  generate_pki "$direct_host" "$device"

  local direct_listen fp token
  if [[ "$direct_enabled" == yes ]]; then direct_listen=":${DIRECT_PORT}"; else direct_listen="127.0.0.1:${DIRECT_PORT}"; fi
  fp="$(client_cert_fingerprint "$PKI_ROOT/${device}.crt")"
  token="$(gen_subscribe_token)"
  write_server_config "$direct_listen" "$device" "$fp" "$cdn_host" "$subscribe_host" "$token"
  install_server_service
  setup_cloudflare_tunnel "$cdn_host" "$tunnel_name"
  tunnel_id="$SETUP_TUNNEL_ID"
  save_state "$cdn_host" "$direct_enabled" "$direct_host" "$DIRECT_PORT" "$tunnel_id" "$subscribe_host"
  archive="$(make_client_bundle "$device" "$token" "$cf_id" "$cf_secret")"

  printf "\n${C_GREEN}${C_BOLD}部署完成。${C_RESET}\n\n"
  printf 'Cloudflare 代理入口：wss://%s%s\n' "$cdn_host" "$WS_PATH"
  printf '订阅地址：https://%s/_lynx/v1/subscribe/%s\n' "$subscribe_host" "$token"
  if [[ "$direct_enabled" == yes ]]; then
    printf '直连地址：%s:%s/TCP\n' "$direct_host" "$DIRECT_PORT"
    warn "请确认云服务器安全组已开放 TCP ${DIRECT_PORT}，并且 ${direct_host} 为 DNS only 灰云记录。"
  fi
  print_nginx_subscribe_hint "$subscribe_host"
  printf "\n客户端包（含 subscribe_url 的单文件 client.json）：${C_BOLD}%s${C_RESET}\n" "$archive"
  printf 'Linux：lynx-client -subscribe \"https://%s/_lynx/v1/subscribe/%s\"\n\n' "$subscribe_host" "$token"
  printf '以后添加设备：sudo lynx-wizard → 选择“添加客户端设备”\n'
}

run_upgrade_server() {
  banner
  need_root
  local root tag="${1:-latest}" script
  root="$(find_root 2>/dev/null || true)"
  script=""
  if [[ -n "$root" && -x "$root/deploy/upgrade-server.sh" ]]; then
    script="$root/deploy/upgrade-server.sh"
  elif [[ -x /usr/local/lib/lynx/upgrade-server.sh ]]; then
    script=/usr/local/lib/lynx/upgrade-server.sh
  else
    die "找不到 deploy/upgrade-server.sh"
  fi
  "$script" "$tag"
}

run_upgrade_client() {
  banner
  need_root
  local root tag="${1:-latest}" script
  root="$(find_root 2>/dev/null || true)"
  script=""
  if [[ -n "$root" && -x "$root/deploy/upgrade-client.sh" ]]; then
    script="$root/deploy/upgrade-client.sh"
  elif [[ -x /usr/local/lib/lynx/upgrade-client.sh ]]; then
    script=/usr/local/lib/lynx/upgrade-client.sh
  else
    die "找不到 deploy/upgrade-client.sh"
  fi
  "$script" "$tag"
}

add_device() {
  banner
  need_root
  [[ -f "$STATE_FILE" && -f "$SERVER_CONFIG" ]] || die "本机尚未完成 Lynx 服务端部署。"
  local device fp token="" cf_id="" cf_secret="" archive
  while true; do
    device="$(ask "新设备名称，例如 phone 或 tablet")"
    validate_device "$device" || { warn "设备名称格式不正确。"; continue; }
    if python3 - "$SERVER_CONFIG" "$device" <<'PY'
import json,sys
with open(sys.argv[1]) as f:d=json.load(f)
raise SystemExit(0 if sys.argv[2] in d.get('clients',{}) else 1)
PY
    then
      if confirm "设备 $device 已存在，是否使用原证书重新生成代理客户端包？" "Y"; then
        [[ -f "$PKI_ROOT/${device}.crt" && -f "$PKI_ROOT/${device}.key" ]] || die "找不到该设备原证书。"
        fp="$(client_cert_fingerprint "$PKI_ROOT/${device}.crt")"
        token="$(add_client_mapping "$device" "$fp" "")"
        break
      fi
    else
      generate_client_cert "$device"
      fp="$(client_cert_fingerprint "$PKI_ROOT/${device}.crt")"
      token="$(gen_subscribe_token)"
      token="$(add_client_mapping "$device" "$fp" "$token")"
      break
    fi
  done
  if confirm "该设备是否使用 Cloudflare Access Service Token？" "N"; then
    cf_id="$(ask "CF-Access-Client-Id")"
    cf_secret="$(ask_secret "CF-Access-Client-Secret")"
  fi
  archive="$(make_client_bundle "$device" "$token" "$cf_id" "$cf_secret")"
  printf '\n${C_GREEN}设备添加完成。${C_RESET}\n'
  printf '设备：%s\n指纹：%s\n订阅地址：%s\n客户端包：%s\n' \
    "$device" "$fp" "$(subscribe_url_for "$token")" "$archive"
}

install_client_from_bundle() {
  banner
  need_root
  die "请在服务端生成的客户端包目录中运行：sudo ./install-client.sh"
}

status_view() {
  banner
  need_root
  echo "Lynx server:"
  systemctl --no-pager --full status lynx-server 2>/dev/null || true
  echo
  echo "Cloudflare tunnel:"
  systemctl --no-pager --full status lynx-cloudflared 2>/dev/null || true
  echo
  if [[ -f "$STATE_FILE" ]]; then
    # shellcheck disable=SC1090
    source "$STATE_FILE"
    echo "CDN endpoint: wss://${CDN_HOST}${WS_PATH}"
    [[ "$DIRECT_ENABLED" == yes ]] && echo "Direct endpoint: ${DIRECT_HOST}:${DIRECT_PORT}"
  fi
  echo
  echo "Recent server logs:"
  journalctl -u lynx-server -n 30 --no-pager 2>/dev/null || true
}

run_uninstall_client() {
  banner
  need_root
  local root script
  root="$(find_root 2>/dev/null || true)"
  script=""
  if [[ -n "$root" && -x "$root/deploy/uninstall-client.sh" ]]; then
    script="$root/deploy/uninstall-client.sh"
  elif [[ -x /usr/local/lib/lynx/uninstall-client.sh ]]; then
    script=/usr/local/lib/lynx/uninstall-client.sh
  else
    die "找不到 deploy/uninstall-client.sh"
  fi
  exec "$script"
}

uninstall_all() {
  banner
  need_root
  warn "此操作将停止服务并删除 /etc/lynx，包括私人 CA、设备证书，以及含内联 PEM 的 client.json。"
  confirm "确定彻底卸载 Lynx？" "N" || exit 0
  systemctl disable --now lynx-client lynx-server lynx-cloudflared 2>/dev/null || true
  # legacy LynxVPN unit names
  systemctl disable --now lynxvpn-client lynxvpn-server lynxvpn-cloudflared 2>/dev/null || true
  rm -f /etc/systemd/system/lynx-client.service /etc/systemd/system/lynx-server.service /etc/systemd/system/lynx-cloudflared.service
  rm -f /etc/systemd/system/lynxvpn-client.service /etc/systemd/system/lynxvpn-server.service /etc/systemd/system/lynxvpn-cloudflared.service
  rm -f /usr/local/bin/lynx-client /usr/local/bin/lynx-server /usr/local/sbin/lynx-wizard
  rm -f /usr/local/bin/lynxvpn-client /usr/local/bin/lynxvpn-server /usr/local/sbin/lynxvpn-wizard
  rm -rf "$INSTALL_ROOT" "$RUNTIME_ROOT" /etc/lynxvpn /usr/local/lib/lynxvpn /var/lib/lynx/subscribe-cache
  systemctl daemon-reload
  if confirm "是否同时删除系统用户 lynx 与 /var/lib/lynx？" "N"; then
    userdel lynx 2>/dev/null || true
    rm -rf /var/lib/lynx
  fi
  ok "Lynx 已卸载；cloudflared 软件本身未删除。详见 docs/uninstall.md"
}

main_menu() {
  banner
  cat <<'EOF'
请选择操作：

  1) 首次部署服务端（自动配置 Cloudflare Tunnel）
  2) 添加或重新生成客户端设备安装包
  3) 查看服务状态和日志
  4) 查看订阅 URL 并探测本机 origin
  5) 一键升级服务端二进制（GitHub Release）
  6) 一键升级本机客户端二进制（GitHub Release）
  7) 仅卸载本机客户端
  8) 卸载 Lynx（服务端+客户端）
  0) 退出

EOF
  local choice
  read -r -p "请输入编号 [1]: " choice
  choice="${choice:-1}"
  case "$choice" in
    1) server_install ;;
    2) add_device ;;
    3) status_view ;;
    4) show_subscribe ;;
    5) run_upgrade_server latest ;;
    6) run_upgrade_client latest ;;
    7) run_uninstall_client ;;
    8) uninstall_all ;;
    0) exit 0 ;;
    *) die "无效选项：$choice" ;;
  esac
}

if [[ "${LYNX_SOURCE_ONLY:-0}" != "1" ]]; then
case "${1:-}" in
  --server) server_install ;;
  --add-device) add_device ;;
  --status) status_view ;;
  --uninstall) uninstall_all ;;
  --uninstall-client) run_uninstall_client ;;
  --show-subscribe) show_subscribe ;;
  --upgrade-server) run_upgrade_server "${2:-latest}" ;;
  --upgrade-client) run_upgrade_client "${2:-latest}" ;;
  --version) echo "$VERSION" ;;
  --help|-h)
    cat <<EOF
用法：sudo $0 [选项]
  无参数          打开交互式菜单
  --server        首次部署服务端
  --add-device    添加客户端设备
  --status        查看状态
  --uninstall     卸载服务端+客户端
  --uninstall-client 仅卸载本机客户端（保留服务端）
  --show-subscribe 打印订阅 URL 并探测本机 127.0.0.1:8080
  --upgrade-server [tag]  从 GitHub Release 升级服务端（默认 latest）
  --upgrade-client [tag]  从 GitHub Release 升级客户端（默认 latest）
EOF
    ;;
  "") main_menu ;;
  *) die "未知参数：$1" ;;
esac
fi
