# Troubleshooting

Work from the network tunnel to the consumer. No real VPN profile is included in this repository.

| Symptom | Check | Typical cause / next step |
| --- | --- | --- |
| `vpn` never starts; `vpn-config` exited with an error | `docker compose logs vpn-config` | `no VPN profile`: set `OPENVPN_CONFIG_BASE64` (or provide `vpn/client.ovpn`); `not valid base64`: re-encode on one line (`base64 -i` / `base64 -w0`); `profile has no remote line`: wrong file; `remote on line N: cannot resolve host`: DNS of the Docker host cannot resolve the VPN server name |
| No `/dev/net/tun` | `ls -l /dev/net/tun` | Docker host/VPS lacks TUN device access |
| Gluetun retries connection | `docker compose logs --tail=150 vpn` | Wrong `remote`, TLS/auth issues, CA file path, unreachable VPN server |
| Gluetun: "host is not an IP address" | `docker compose logs vpn-config` | The profile did not go through `vpn-config` (old compose file or volume mounted by hand); deploy with the current `compose.yaml` / `compose.portainer.yaml` |
| VPN worked, stopped after the server's IP changed | `docker compose logs vpn` | Redeploy (`make deploy` / Portainer «Update the stack»): `vpn-config` resolves the DDNS name again; a plain restart reuses the old address |
| Portainer cannot pull `ghcr.io/aquare11e/karusel-tuner` | Portainer stack logs | Image not built yet (see the repository's Actions) or the GHCR package is private: make it public or add GHCR credentials in Portainer |
| `curl healthz` OK, Plex black screen | `make smoke-video`, relay logs | Liveness is not readiness; source/VPN/player may be offline |
| Log: `upstream returned no video: resolve MediaVitrina stream: streams API URL not found in player page (player changed?)` | `docker compose logs --tail=100 relay` | The MediaVitrina player page changed; verify the official player URL and update `SOURCE_URL` or the resolver |
| Log: `... no unprotected HLS stream offered for country "XX" (DRM formats are not supported)` | relay logs | Only DRM variants are offered for this egress; the project does not bypass DRM |
| Log: `... geo-restricted placeholder for country "XX": check the VPN egress` | relay logs, VPN location | VPN egress is not in an allowed country; use an authorized egress |
| Log: `... streams API: HTTP 409` (or another code) | relay logs | Balancer refused the request (e.g. missing/changed Referer); the player flow changed |
| Log prefix `startup timeout: ` | relay logs | Resolving the stream plus first bytes took longer than `STARTUP_TIMEOUT` (45s) |
| Streamlink: "No playable streams found" | relay logs | Only for a `SOURCE_URL` that is not a `player.mediavitrina.ru/.../player.html` page: Streamlink has no matching plugin |
| `/live.ts` HTTP 502 | `docker compose logs --tail=100 relay` | No initial bytes before `STARTUP_TIMEOUT` or source unavailable |
| `/live.ts` HTTP 503 | `curl http://127.0.0.1:18080/status` | All `MAX_VIEWERS` tuners in use; stop a viewer/recording |
| Plex does not find the tuner | Plex DVR setup | No UDP discovery: enter `127.0.0.1:18080` manually; check `curl http://127.0.0.1:18080/discover.json` |
| Plex guide empty | `/status` → `epg` | `epg.last_error` shows why; `/epg.xml` is 503 until the first download; first retry after ~1 min |
| Guide shifted by hours | Compare a programme boundary with the stream | Web feed follows another regional schedule; change the time zone in `EPG_SOURCE_URL` |
| Recording stops early | `/status` → `upstream_restarts`, relay logs ("upstream ended (exit status …)") | Source dropped and did not recover within `RECONNECT_WINDOW` (5m) |
| Recording has a short gap | relay logs "upstream restarted" | Expected: Streamlink was restarted inside the same stream |
| Plex container cannot reach the relay | `curl` from inside the Plex container | Bridged Plex: `127.0.0.1` means Plex itself; use host networking or `vpn:8080` on the project network |
| ffprobe no valid codec | `ffprobe -v error sample.ts` | Upstream failed or TS container incompatible; remux may be needed (future P1) |
| Stutters or reconnect loops | `docker compose logs -f relay` | VPN bandwidth (stream ≈ 7 Mbit/s), HLS quality, tokens changing |

## Verify without sending credentials to anyone

```bash
docker compose ps
docker compose logs --tail=100 vpn relay
curl -v --max-time 5 http://127.0.0.1:18080/healthz
curl --max-time 5 http://127.0.0.1:18080/status
# Capture just a short sample; examine locally, do not upload copyrighted video
SMOKE_VIDEO=1 bash scripts/smoke.sh
```

Streamlink warnings in the relay log can include signed HLS URLs with tokens. Review all logs **before** sharing them: remove IPs you consider private, VPN usernames, passwords, signed HLS URLs, bearer tokens and session cookies. `docker compose config` might print interpolated secrets; use `docker compose config -q` to validate without dumping config.

## Sources

- Streamlink MediaVitrina plugin: https://github.com/streamlink/streamlink/blob/master/src/streamlink/plugins/mediavitrina.py
- Gluetun configuration: https://github.com/qdm12/gluetun/wiki
