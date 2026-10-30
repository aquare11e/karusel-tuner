#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

command -v docker >/dev/null || { echo 'ERROR: install Docker Engine + Compose plugin' >&2; exit 1; }
docker compose version >/dev/null || { echo 'ERROR: Docker Compose v2 is required' >&2; exit 1; }
# On Linux the host must expose TUN to Docker. Docker Desktop (macOS) provides
# it inside its own VM, so there is no host device to check.
if [[ "$(uname -s)" == Linux ]]; then
    [[ -c /dev/net/tun ]] || { echo 'ERROR: /dev/net/tun not found (enable TUN device)' >&2; exit 1; }
fi
[[ -s vpn/client.ovpn ]] || { echo 'ERROR: copy your OpenVPN profile to vpn/client.ovpn' >&2; exit 1; }
if [[ ! -f .env ]]; then
    cp .env.example .env
    chmod 600 .env
    echo 'Created .env from example. Review its settings and re-run this script.' >&2
    exit 1
fi

if [[ "${LOCAL_PLEX:-0}" == "1" ]]; then
    # Local test bench: adds a Plex container that reaches the relay as vpn:8080.
    mkdir -p plex-local-data/recordings
    docker compose -f compose.yaml -f compose.local-plex.yaml config -q
    docker compose -f compose.yaml -f compose.local-plex.yaml up -d --build
    echo 'Local Plex: http://127.0.0.1:32400/web (tuner address vpn:8080)'
else
    docker compose config -q
    docker compose up -d --build vpn relay
fi

echo 'Deployed. Check: docker compose ps && docker compose logs --tail=100 vpn relay'
echo 'Smoke test: bash scripts/smoke.sh'
