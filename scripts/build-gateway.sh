#!/bin/bash
set -euo pipefail

readonly PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly OUTPUT="${PROJECT_ROOT}/packaging/emby/app/server/gateway-proxy"

mkdir -p "$(dirname "${OUTPUT}")"
(
  cd "${PROJECT_ROOT}/gateway"
  go test ./...
  go vet ./...
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "${OUTPUT}" .
)
chmod 0755 "${OUTPUT}"
