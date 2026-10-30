# Architecture and design constraints

## Data flow

```text
MediaVitrina player page (SOURCE_URL)
    -> Go relay resolves the plain-HLS playlist via the player's streams API
    -> Streamlink CLI subprocess plays hls://<playlist> best
    -> stdout (MPEG-TS bytes)
    -> Go net/http GET /live.ts (TCP 8080)
    -> Docker port mapping on vpn: 127.0.0.1:18080
    -> Plex Live TV (relay added as an HDHomeRun tuner by address)

karusel-tv.ru schedule JSON
    -> Go in-memory guide (refreshed every EPG_REFRESH)
    -> GET /epg.xml (XMLTV) -> Plex guide
```

The relay container uses `network_mode: service:vpn`, so every network request it makes — the player page, the streams API, Streamlink's HLS traffic and schedule downloads — leaves through the Gluetun OpenVPN tunnel.

**VPN scope:** only the relay is inside the VPN namespace. Plex and the host keep their ordinary routes. Verified 2026-10-30: the relay's egress was in Russia while the host's egress stayed in its own country.

**Plex ingress:** Plex talks to the relay at `PUBLIC_BASE_URL` (`/discover.json`, `/lineup.json`, `/live.ts`, `/epg.xml`). `127.0.0.1` is reachable only for Plex on the Linux host itself or with host networking; a bridged Plex container needs an address it can reach — see the deployment docs.

## Stream source (MediaVitrina)

Streamlink's `mediavitrina` plugin scrapes the player page for an `api/v3/.../playlist/..._as_array.json` URL. The current player no longer contains it, so Streamlink 8.6.3 (latest at the time of writing) fails with `invalid url_json` / `No playable streams found`. The relay resolves the stream itself, the same way the official player does (`internal/relay/mediavitrina.go`):

1. GET `SOURCE_URL` (the player page) and read `remoteConfig.streams_api_v2_url` (`media.mediavitrina.ru/balancer/v3/.../streams.json`).
2. Fill `{{PLAYER_REFERER_HOSTNAME}}` with the player's host, blank the other `{{...}}` placeholders, and GET it with `Referer: SOURCE_URL`. Without the Referer the API answers HTTP 409.
3. The response lists formats per protocol: `hls` (plain HLS; segments use standard HLS AES-128 with a key from the CDN, no DRM), `hlsp` and `mpdp` (Widevine/FairPlay DRM) and `mpd`. The relay takes `hls[0]` and never selects the DRM formats.
4. Streamlink receives `hls://<playlist> best`: up to 1080p25 H.264 + AAC at about 7 Mbit/s, roughly 3 GB per hour.

Resolution runs before every Streamlink start and restart because the playlist tokens are short-lived, and it counts against `STARTUP_TIMEOUT`. Any other `SOURCE_URL` (for example a URL handled by another Streamlink plugin) goes to Streamlink unchanged.

Signals from the streams API that the relay turns into errors (HTTP 502 on `/live.ts`, reason in the relay log):

- `hls` is empty and only DRM formats are offered: `no unprotected HLS stream offered`. Not supported by design.
- The egress is outside Russia (the player config has `allowed_countries: ["RU"]`): `hls` holds a `/georestrictions/` placeholder clip, which the relay refuses with `geo-restricted placeholder for country ...: check the VPN egress`, so it never records the notice instead of the channel.
- The player page changed: `streams API URL not found in player page`.

## Programme guide

- Source: `EPG_SOURCE_URL` (default `https://www.karusel-tv.ru/schedule/Europe/Moscow`), one JSON per day (`/<yyyy>/<mm>/<dd>/nearest_events.json`), from yesterday up to `EPG_DAYS` ahead or the end of the published horizon (about 11 days on 2026-10-30).
- Kept in memory; refreshed every `EPG_REFRESH`. After a failure it retries from 1 minute, doubling up to `EPG_REFRESH` but never longer than 30 minutes, and keeps the last good guide.
- `/epg.xml` answers 503 with `Retry-After` until the first successful download, so Plex never receives an empty guide.
- The relay can start before the tunnel is up; the first download may fail (observed: `TLS handshake timeout`), and the 1-minute retry covers it.
- Timing of the web stream: the streams API reports the client's region (for example a Siberian one) but `target_client_timezone_sec=10800` and `orbits: []`, i.e. a single Moscow-timed feed. Checked 2026-10-30 22:02–22:15 MSK: on-air content and the ad break at the 22:05 boundary matched the Moscow schedule, and karusel-tv.ru matched the player's own EPG (`static-api.mediavitrina.ru/v1/epg/carusel/lite_web.json`, about one day only, therefore not used as the source) for 23 of 23 programmes.

## Go lifecycle

- Config is read once on startup; bad values fail fast.
- `/live.ts` reserves one of `MAX_VIEWERS` slots atomically; overflow returns 503 without starting anything. `HEAD /live.ts` never starts Streamlink.
- One `STARTUP_TIMEOUT` deadline covers source resolution and the first video bytes. No status 200 before the first bytes; failure returns 502.
- Streamlink runs via argv (`exec.CommandContext`) in its own process group (`Setpgid`); cancellation SIGKILLs the whole group.
- After 200, if Streamlink exits, the relay resolves the source again and restarts Streamlink with backoff (1 s doubling to 30 s; reset after a run that streamed for at least 30 s, so a flapping upstream keeps backing off) for up to `RECONNECT_WINDOW`, appending to the same response (an MPEG-TS discontinuity). After the window it closes the response.
- Response flushing is periodic; live connections have no global `WriteTimeout`.
- Request disconnect or SIGTERM cancels Streamlink and its children; the app performs a bounded HTTP shutdown.
- One viewer opens one Streamlink upstream (about 65 MB RSS measured); this is the current scaling limit.

## Failure matrix

| Failure | Current behaviour | Future handling |
| --- | --- | --- |
| VPN not up yet or down | Gluetun firewall blocks all egress; `/healthz` 200; `/live.ts` 502; guide retries from 1 min (max 30 min) | Separate readiness, health monitoring |
| VPN egress outside Russia | `/live.ts` 502, log `geo-restricted placeholder` | Fix the VPN egress |
| Player page format changed | `/live.ts` 502, log `streams API URL not found` | Update the resolver |
| Only DRM formats offered | `/live.ts` 502, log `no unprotected HLS stream offered` | Not supported; do not bypass DRM |
| Client disconnect | Kill process group, free slot | Share one upstream between viewers |
| Streamlink stops after writing | Re-resolve and restart within `RECONNECT_WINDOW` in the same response, then close | — |
| N+1 viewers | HTTP 503 | Shared bounded fan-out |
| Schedule site unavailable | Keep the last guide; `/epg.xml` 503 only before the first success | — |
| Plex cannot reach `PUBLIC_BASE_URL` | URL/network mismatch | Explicit LAN/host-gateway setup |

## Known environment assumptions

Verified on 2026-10-30 unless marked otherwise.

- Docker Engine + Compose v2 with TUN. Linux is the production target; Docker Desktop on macOS also runs the stack (TUN exists inside its VM, not as `/dev/net/tun` on the host).
- Gluetun's custom OpenVPN provider requires an IP literal in `remote`; a hostname fails with `host is not an IP address`. A profile that points at a dynamic-DNS name must be re-resolved whenever that address changes.
- The VPN egress must be in Russia (`allowed_countries: ["RU"]` in the player config).
- Resource use: Gluetun about 40 MiB; relay about 16 MiB idle and about 60 MiB with one viewer (Streamlink about 67 MB RSS).
- Not yet verified: whether the web stream shows «Спокойной ночи, малыши!» at 21:00 MSK or a rights placeholder; recording through Plex end to end.
- Go 1.23+ for local test/build. The Docker image builds with Go 1.23 bookworm.

## Privacy and security

- Bind the local port to `127.0.0.1` unless explicitly approved for a trusted LAN.
- Never log cookies, signed HLS URLs, passwords, profile contents or auth token responses. Resolver errors omit URLs. Streamlink's stderr passes through a line filter that strips URL query strings (`?<redacted>`), where the playlist tokens live; scheme, host and path stay for diagnosis. The resolved playlist URL is still visible in the Streamlink process arguments (`ps`, `/proc/*/cmdline`) to users of the Docker host while it runs; tokens are short-lived, but treat host shell access accordingly.
- `.env`, `vpn/*` excluded by git and `.dockerignore`.
- Gluetun firewall is kept enabled for kill-switch behavior (outbound policy DROP; only loopback, the Docker subnet, the VPN server and `tun0` are allowed). Do not introduce a fallback to plain network routing.
- The project is for personal/local use where the user has rights; no DRM bypass, exposed public rebroadcast or credentials harvesting.
