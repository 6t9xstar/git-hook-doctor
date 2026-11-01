#!/usr/bin/env bash
# Cross-compile release archives into dist/: ./scripts/build.sh 1.0.0
set -euo pipefail
VERSION="${1:?usage: build.sh <version>}"
rm -rf dist && mkdir -p dist
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os="${target%/*}"; arch="${target#*/}"
  name="githookdoctor_${VERSION}_${os}_${arch}"
  ext=""; [ "$os" = windows ] && ext=".exe"
  mkdir -p "dist/$name"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "dist/$name/githookdoctor$ext" ./cmd/githookdoctor
  cp LICENSE README.md "dist/$name/"
  if [ "$os" = windows ]; then
    (cd dist && zip -qr "$name.zip" "$name")
  else
    tar -czf "dist/$name.tar.gz" -C dist "$name"
  fi
  rm -rf "dist/$name"
done
(cd dist && sha256sum * > checksums.txt)
ls -l dist
