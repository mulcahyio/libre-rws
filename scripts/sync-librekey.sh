#!/usr/bin/env bash
# Refresh third_party/librekey from a LibreKey checkout (default ../librekey).
set -euo pipefail
src=${1:-../librekey}; dst=third_party/librekey
cd "$(dirname "$0")/.."
for p in client license fingerprint request; do rm -rf "$dst/$p"; mkdir -p "$dst/$p"; cp "$src/$p"/*.go "$dst/$p/"; done
sed -i "s/at commit [0-9a-f]*/at commit $(git -C "$src" rev-parse --short HEAD)/" "$dst/LIBREKEY_COPY.md"
echo "synced from $src"
