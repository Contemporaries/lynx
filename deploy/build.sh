#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
mkdir -p dist
export PATH="${HOME}/.local/go/bin:${PATH}"

VERSION="${LYNX_VERSION:-}"
if [[ -z "$VERSION" ]]; then
  VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
fi
LDFLAGS="-s -w -X github.com/Contemporaries/lynx/internal/version.Version=${VERSION}"

build_one() {
  local os="$1" arch="$2" pkg="$3" out="$4"
  echo "building $out (GOOS=$os GOARCH=$arch VERSION=$VERSION)"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags="$LDFLAGS" -o "dist/$out" "./cmd/$pkg"
}

build_one linux amd64 lynx-server lynx-server-linux-amd64
build_one linux arm64 lynx-server lynx-server-linux-arm64
build_one linux amd64 lynx-client lynx-client-linux-amd64
build_one linux arm64 lynx-client lynx-client-linux-arm64
build_one windows amd64 lynx-client lynx-client-windows-amd64.exe
build_one linux amd64 lynx-web-server lynx-web-server-linux-amd64
build_one linux arm64 lynx-web-server lynx-web-server-linux-arm64
build_one linux amd64 lynx-web-client lynx-web-client-linux-amd64
build_one linux arm64 lynx-web-client lynx-web-client-linux-arm64

cp -f dist/lynx-server-linux-amd64 dist/lynx-server
cp -f dist/lynx-client-linux-amd64 dist/lynx-client
cp -f dist/lynx-web-server-linux-amd64 dist/lynx-web-server
cp -f dist/lynx-web-client-linux-amd64 dist/lynx-web-client

(
  cd dist
  sha256sum \
    lynx-server lynx-client lynx-web-server lynx-web-client \
    lynx-server-linux-amd64 lynx-server-linux-arm64 \
    lynx-client-linux-amd64 lynx-client-linux-arm64 \
    lynx-client-windows-amd64.exe \
    lynx-web-server-linux-amd64 lynx-web-server-linux-arm64 \
    lynx-web-client-linux-amd64 lynx-web-client-linux-arm64 \
    > SHA256SUMS
)
echo "artifacts written to dist/ (version=$VERSION)"
cat dist/SHA256SUMS
