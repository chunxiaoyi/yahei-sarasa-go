#!/bin/sh
# Cross-compiles the Windows binaries into dist/ (works on any OS with Go 1.22+).
#   YaHeiSarasa-<arch>.exe       GUI (no console window)
#   yahei-sarasa-cli-<arch>.exe  console build for scripting
set -eu
cd "$(dirname "$0")"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
LD="-s -w -X github.com/chunxiaoyi/yahei-sarasa-go/internal/app.Version=$VERSION"
mkdir -p dist
for arch in amd64 arm64; do
  GOOS=windows GOARCH=$arch CGO_ENABLED=0 go build -trimpath -ldflags "$LD -H=windowsgui" -o "dist/YaHeiSarasa-$arch.exe" .
  GOOS=windows GOARCH=$arch CGO_ENABLED=0 go build -trimpath -ldflags "$LD" -o "dist/yahei-sarasa-cli-$arch.exe" .
done
(cd dist && sha256sum *.exe > SHA256SUMS.txt 2>/dev/null || shasum -a 256 *.exe > SHA256SUMS.txt)
ls -l dist
