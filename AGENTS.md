# AGENTS.md — instructions for Codex

## Objective
Develop and maintain a Linux-first, local-only Karusel → Plex live relay in Go. An OpenVPN namespace (Gluetun) fetches the authorized MediaVitrina/HLS stream via Streamlink; the Go HTTP service is itself an HDHomeRun-compatible tuner for Plex (MPEG-TS, lineup, XMLTV guide) and still serves an M3U for IPTV players.

## Important truths (do not overwrite these with guesses)
- This repository is an **MVP**, not a proven 24/7 deployment. Real access needs the owner's VPN config and endpoint confirmation.
- `GET /healthz` is **process liveness**, not VPN readiness. Do not say otherwise.
- `SOURCE_URL` may need updating if the official site changes. Do not hardcode short-lived HLS tokens.
- Plex DVR talks to the relay directly as an HDHomeRun tuner (`/discover.json`, `/lineup.json`) and reads the guide from `/epg.xml`. Threadfin is no longer part of the project. There is no UDP discovery: the tuner is added in Plex by address.
- The default deployment deliberately publishes relay only on 127.0.0.1:18080; production Plex uses `network_mode: host` on the same Linux host. `compose.local-plex.yaml` is a local test bench only (Plex reaches `vpn:8080`).
- Streamlink's MediaVitrina plugin no longer works with the current player (2026-10); for `player.mediavitrina.ru` URLs the relay resolves the HLS URL from the balancer API itself and hands Streamlink an `hls://` URL. Only the unprotected `hls` variant is used, never DRM variants.
- The guide comes from karusel-tv.ru schedule JSON (undocumented, may change). Programmes air in blocks; a block cannot be split into episodes.
- After first bytes, an upstream exit restarts Streamlink inside the same `/live.ts` response for up to `RECONNECT_WINDOW`; Plex recordings rely on this.
- The default implementation starts one Streamlink child per active viewer; no shared ring buffer yet.

## Rules for code changes
1. Keep Go standard library preferred; do not add dependencies without a good reason.
2. Use `exec.CommandContext` with **argv**; never use shell interpolation of a source URL or secrets.
3. All Streamlink children must exit on viewer disconnect, startup timeout, and SIGTERM. Kill process groups on Linux when necessary. Never leave zombie helpers.
4. Never route Plex or the general host through the VPN. The `relay` container must keep `network_mode: service:vpn`.
5. Do not expose HTTP endpoints beyond loopback by default; document auth/network restrictions before enabling LAN access.
6. Preserve backward compatibility for `/healthz`, `/playlist.m3u`, `/live.ts`, `/status` and the `.env` names.
7. No VPN credentials, real `.ovpn`, keys, tokens, network identifiers or personal data in code, examples, tests, screenshots or logs. Keep `.env` and `vpn/*` gitignored.
8. Do not remove Docker firewall/kill-switch settings to make a test pass. Never weaken TLS verification or bypass DRM/access controls.
9. For behavior changes, add table-driven tests or `httptest` tests with a **fake Streamlink binary**; no network or real VPN in unit tests.
10. Keep user-facing instructions in Russian and code comments/identifiers in English.

## Working commands
```bash
make check           # go fmt, vet, race tests (and docker compose config when available)
make build           # native Go binary; requires 'streamlink' to be installed for live streaming
make deploy          # checks Docker + TUN + ovpn, then starts vpn+relay
make local-up        # local test bench: VPN + relay + Plex (compose.local-plex.yaml)
make local-down
make smoke           # local HTTP status/playlist
make smoke-video     # optional, needs real authorized stream through VPN
make logs
make down
```

## Code map
- `cmd/relay/main.go` — config and SIGTERM-aware HTTP server lifecycle.
- `internal/relay/config.go` — config defaults and validation.
- `internal/relay/handler.go` — HTTP endpoints, status, viewer cap.
- `internal/relay/upstream.go` — Streamlink attempts, restart loop, process-group lifecycle.
- `internal/relay/hdhr.go` — HDHomeRun tuner endpoints for Plex.
- `internal/relay/mediavitrina.go` — MediaVitrina balancer resolver.
- `internal/epg/` — schedule download, in-memory guide with retries, XMLTV rendering.
- `internal/relay/*_test.go` — isolated CLI-mock and HTTP tests.
- `compose.yaml` — VPN and relay. `compose.local-plex.yaml` — local Plex test bench.
- `docs/` — architecture, Linux deployment, troubleshooting.
- `TASKS.md` — project backlog/acceptance criteria.

## Development steps
1. Read `README.md`, `TASKS.md`, `docs/architecture.md` before modifying architecture.
2. Run `make check` first; report missing local tools (especially Docker) instead of claiming integration tests passed.
3. Pick one task from `TASKS.md`, implement it with tests, run `make check`.
4. If changing `.env` or `compose.yaml`, update docs and explain how to upgrade a deployed installation without losing VPN config or Plex DVR setup.
5. Report modified files, verification commands/results, and unverified real-world steps separately.

## Definition of done
- Tests including `go test -race ./...` pass; no leaking child processes.
- Works with `docker compose config -q` where Docker is present.
- Safe by default: no secret commits, no unintentional public ports.
- New features include user documentation and failure-mode behavior.
- Honest status on real VPN/stream/Plex validation; never invent successful end-to-end tests.
