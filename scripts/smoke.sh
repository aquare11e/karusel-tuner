#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
if [[ -f .env ]]; then
  # This script reads RELAY_PORT without sourcing secrets in .env.
  RELAY_PORT="$(sed -nE 's/^RELAY_PORT=([0-9]+)$/\1/p' .env | tail -1)"
fi
PORT="${RELAY_PORT:-18080}"
BASE="http://127.0.0.1:${PORT}"
echo "Checking ${BASE}"
curl -fsS --max-time 5 "${BASE}/healthz"
curl -fsS --max-time 5 "${BASE}/status"
echo
curl -fsS --max-time 5 "${BASE}/playlist.m3u"
echo
curl -fsS --max-time 5 "${BASE}/discover.json"
curl -fsS --max-time 5 "${BASE}/lineup.json"
EPG_CODE="$(curl -sS --max-time 10 -o /dev/null -w '%{http_code}' "${BASE}/epg.xml" || true)"
if [[ "${EPG_CODE}" == 200 ]]; then
  echo '/epg.xml: 200'
else
  # 503 right after start is normal until the first schedule download succeeds.
  echo "WARNING: /epg.xml returned ${EPG_CODE}; see the epg block in /status" >&2
fi
if [[ "${SMOKE_VIDEO:-0}" == "1" ]]; then
  TMP="$(mktemp "${TMPDIR:-/tmp}/karusel-smoke.XXXXXX")"
  trap 'rm -f "$TMP"' EXIT
  echo 'Capturing a short video sample (curl timeout after 20 seconds is expected)...'
  curl -fsS --max-time 20 "${BASE}/live.ts" -o "$TMP" || test "$?" -eq 28
  [[ -s "$TMP" ]] || { echo 'ERROR: no video data received' >&2; exit 1; }
  echo "Captured $(wc -c < "$TMP") bytes"
  PROBE=(-v error -show_entries stream=codec_name,codec_type -of compact)
  if command -v ffprobe >/dev/null 2>&1; then
    ffprobe "${PROBE[@]}" "$TMP"
  elif [[ -n "$(docker compose ps -q --status running relay 2>/dev/null)" ]]; then
    # The relay image ships ffmpeg; feed the sample through stdin.
    docker compose exec -T relay ffprobe "${PROBE[@]}" -i pipe:0 < "$TMP"
  else
    echo 'ffprobe not available; video container not validated' >&2
  fi
fi
