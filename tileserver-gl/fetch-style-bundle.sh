#!/bin/sh
set -eu

: "${RASTER_TILE_TOKEN:?RASTER_TILE_TOKEN is required}"

archive="$(mktemp)"
staging="$(mktemp -d)"
trap 'rm -f "$archive"; rm -rf "$staging"' EXIT

# The backend creates its schema during startup, so retry until its internal
# bundle endpoint is ready rather than relying on container startup ordering.
until curl --fail --silent --show-error \
  --header "X-Forwarded-Uri: /internal/tileserver/style-bundle.tar.gz?token=${RASTER_TILE_TOKEN}" \
  "http://backend:3000/internal/tileserver/style-bundle.tar.gz" \
  --output "$archive"; do
  echo "Waiting for TileServer GL style bundle..." >&2
  sleep 2
done

tar -xzf "$archive" -C "$staging"
test -f "$staging/config.json"

# TileServer GL starts only after a complete snapshot is present. The volume is
# dedicated to generated files, so replacing its contents is safe.
rm -rf /data/*
cp -a "$staging"/. /data/
