#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
go fmt ./cmd/... ./internal/... >/dev/null
go vet ./...
go test -count=1 -race ./...
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  docker compose config -q
  docker compose -f compose.yaml -f compose.local-plex.yaml config -q
  # The Portainer stack requires the profile variable; a placeholder suffices.
  OPENVPN_CONFIG_BASE64=placeholder docker compose -f compose.portainer.yaml config -q
else
  echo 'NOTICE: docker compose not installed; skipping compose syntax check' >&2
fi
