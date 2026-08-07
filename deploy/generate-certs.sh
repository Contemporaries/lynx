#!/usr/bin/env bash
set -euo pipefail

# Generate Lynx PKI. CA private key stays on the management machine by default.
# Usage: ./deploy/generate-certs.sh [direct_domain] [inner_name] [client_device] [out_dir]

DIRECT_DOMAIN="${1:-direct.example.com}"
INNER_NAME="${2:-lynx.internal}"
CLIENT_CN="${3:-laptop}"
OUT="${4:-./certs}"
MANAGE_DIR="${OUT}/manage"

mkdir -p "$OUT" "$MANAGE_DIR"
umask 077

if [[ ! -f "$MANAGE_DIR/ca.key" ]]; then
  openssl genpkey -algorithm ED25519 -out "$MANAGE_DIR/ca.key"
  openssl req -x509 -new -key "$MANAGE_DIR/ca.key" -out "$MANAGE_DIR/ca.crt" -days 3650 -sha256 \
    -subj "/CN=Lynx Private CA"
  echo "Created offline CA under $MANAGE_DIR (keep ca.key offline)."
fi

cp "$MANAGE_DIR/ca.crt" "$OUT/ca.crt"

openssl genpkey -algorithm ED25519 -out "$OUT/server.key"
openssl req -new -key "$OUT/server.key" -out "$OUT/server.csr" -subj "/CN=$INNER_NAME"
cat > "$OUT/server.ext" <<EOT
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=serverAuth
subjectAltName=DNS:$DIRECT_DOMAIN,DNS:$INNER_NAME
EOT
openssl x509 -req -in "$OUT/server.csr" -CA "$MANAGE_DIR/ca.crt" -CAkey "$MANAGE_DIR/ca.key" \
  -CAcreateserial -out "$OUT/server.crt" -days 825 -sha256 -extfile "$OUT/server.ext"

SAFE_CN="$(tr -cd 'A-Za-z0-9._-' <<<"$CLIENT_CN")"
openssl genpkey -algorithm ED25519 -out "$OUT/${SAFE_CN}.key"
openssl req -new -key "$OUT/${SAFE_CN}.key" -out "$OUT/${SAFE_CN}.csr" -subj "/CN=$CLIENT_CN"
cat > "$OUT/client.ext" <<'EOT'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=clientAuth
EOT
openssl x509 -req -in "$OUT/${SAFE_CN}.csr" -CA "$MANAGE_DIR/ca.crt" -CAkey "$MANAGE_DIR/ca.key" \
  -CAcreateserial -out "$OUT/${SAFE_CN}.crt" -days 825 -sha256 -extfile "$OUT/client.ext"

FP="$(openssl x509 -in "$OUT/${SAFE_CN}.crt" -noout -fingerprint -sha256 | sed 's/^.*=//' | tr -d ':' | tr 'A-F' 'a-f')"

rm -f "$OUT"/*.csr "$OUT"/*.ext "$MANAGE_DIR"/*.srl 2>/dev/null || true
chmod 600 "$OUT"/*.key "$MANAGE_DIR"/ca.key
chmod 644 "$OUT"/*.crt "$MANAGE_DIR"/ca.crt

cat > "$OUT/client-auth.snippet.json" <<EOF
{
  "${SAFE_CN}": {
    "certificate_sha256": "${FP}",
    "enabled": true
  }
}
EOF

echo "Server cert SAN: $DIRECT_DOMAIN, $INNER_NAME"
echo "Client device: $CLIENT_CN"
echo "Client certificate SHA-256: $FP"
echo "Deploy to server (no ca.key): $OUT/{ca.crt,server.crt,server.key}"
echo "Keep offline: $MANAGE_DIR/ca.key"
echo "Auth snippet: $OUT/client-auth.snippet.json"
