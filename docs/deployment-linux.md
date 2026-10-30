# Linux deployment and network topologies

## Prerequisites

- Docker Engine with Docker Compose v2: `docker compose version`.
- `/dev/net/tun` exists: `test -c /dev/net/tun && echo OK`.
- A valid `.ovpn` profile and any separate CA/client files, in `vpn/`.
- The profile's `remote` must be an **IP address**: Gluetun rejects host names ("host is not an IP address"), and its firewall blocks DNS before the tunnel is up. For a DDNS name, resolve it to an IP beforehand and update the profile when the address changes.
- `vpn/` is mounted read-only as a directory. To keep the profile elsewhere, replace that mount in a local override (`volumes: !override`); adding a single-file mount inside the read-only directory fails with "read-only file system".
- Local firewall: do not expose ports without intention.
- Enough bandwidth (the stream is ≈ 7 Mbit/s) and authorization for personal viewing. The code does not circumvent DRM.

## Deploy on the same Linux machine as Plex

Plex runs in Docker with `network_mode: host` (or natively) on the same host and reaches the relay at `http://127.0.0.1:18080`. Nothing in Plex's own compose needs to change.

```bash
cp .env.example .env
chmod 600 .env
cp /secure/path/client.ovpn vpn/client.ovpn
# Edit .env: OPENVPN_USER/PASSWORD if necessary, do not commit
make deploy
```

If `deploy.sh` creates `.env` itself, it exits after copying for manual review; run it again after reviewing.

Validate in order:

```bash
docker compose ps
docker compose logs --tail=80 vpn
bash scripts/smoke.sh   # healthz, status, playlist, discover/lineup, /epg.xml
make smoke-video        # ~20 s of video + ffprobe (host or relay container)
```

`/epg.xml` answers 503 until the first schedule download succeeds; `/status` → `epg.last_error` explains why. The first download retries after about a minute if the VPN was not up yet.

### Plex DVR setup

1. Plex → Settings → Live TV & DVR → **Set up Plex DVR**.
2. **"Don't see your HDHomeRun device? Enter its network address manually"** → `127.0.0.1:18080`. UDP discovery is not implemented.
3. Channel `1` «Карусель» appears.
4. Guide: **"Have an XMLTV guide on your server?"** → `http://127.0.0.1:18080/epg.xml`; map channel `1` to «Карусель» if Plex did not do it.
5. Pick a TV library for recordings, then record a series from the guide (padding: start 2 min early, end 3 min late).

## Plex in a bridged Docker container on the same machine

Inside Plex's bridge network `127.0.0.1` is Plex itself. Either attach Plex to this project's network and use `http://vpn:8080` (set `PUBLIC_BASE_URL=http://vpn:8080` so lineup URLs match), or publish the relay on an address the Plex container can reach — see the next section for the security trade-off. Do not put Plex into the VPN namespace.

## Relay use from a different machine on trusted LAN

The safe default is loopback only. If **necessary**, change:

```yaml
# in compose.yaml, vpn.ports
- '192.168.1.10:18080:8080'  # example; use YOUR real Linux LAN IP
```

Then in `.env` set `PUBLIC_BASE_URL=http://192.168.1.10:18080` (Plex follows the lineup URLs, so they must be reachable from it). Apply a firewall rule that permits **only your trusted local subnet**; never publish it on a public IP or router. Anyone with LAN access can then start upstream jobs within the tuner limit; add authentication if the LAN is untrusted.

## Upgrading from the Threadfin setup

Earlier versions shipped an optional Threadfin profile. It is gone: the relay is now the tuner. Skip this section if Threadfin was never started (`docker volume ls | grep threadfin` shows nothing).

```bash
# BEFORE updating the code: stop and remove the old Threadfin container
docker compose --profile threadfin stop threadfin
docker compose --profile threadfin rm -f threadfin
# update the code, then in .env set MAX_VIEWERS=2
docker compose config -q   # Go is not needed on the server; make check is for development
make deploy
```

- The Threadfin volumes (`karusel-plex-relay_threadfin-conf` and `karusel-plex-relay_threadfin-temp`; names may differ with a custom project name) are **not** deleted. Remove it with `docker volume rm` once you no longer need it. Never use `docker compose down -v` casually.
- If you already updated the code and Compose warns about orphan containers, `docker compose up -d --remove-orphans` removes the old Threadfin container (volumes stay).
- In Plex: remove the old Threadfin DVR, add the tuner `127.0.0.1:18080` and the XMLTV guide `http://127.0.0.1:18080/epg.xml` as above, and recreate series recordings — they belonged to the old DVR.

## Updating safely

```bash
git pull
docker compose config -q
make deploy   # rebuild and restart
```

`make check` needs Go and is meant for the development machine, not for the server.

Keep `vpn/client.ovpn` and `.env` backed up securely. Docker may require `sudo` depending on group configuration.

## Rollback / stop

```bash
docker compose down   # named volumes are kept
```

To revert code, switch to a previous Git commit and rerun deployment. Revalidate stream and tuner afterwards.
