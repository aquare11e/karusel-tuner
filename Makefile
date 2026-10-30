SHELL := /bin/bash
.DEFAULT_GOAL := help
.PHONY: help fmt test vet check build up deploy local-up local-down smoke smoke-video logs down

help:
	@printf '%s\n' 'make check       Go fmt, vet, race tests, Compose syntax' \
	  'make build       Build Go application' \
	  'make up          Start VPN + relay (requires vpn/client.ovpn)' \
	  'make deploy      Check prerequisites then deploy VPN + relay' \
	  'make local-up    Local test bench: VPN + relay + Plex (compose.local-plex.yaml)' \
	  'make local-down  Stop the local test bench (volumes kept)' \
	  'make smoke       Test local HTTP endpoints (no upstream)' \
	  'make smoke-video Capture sample and optionally ffprobe (VPN/upstream required)' \
	  'make logs        Follow VPN and relay logs' \
	  'make down        Stop project containers'
fmt:
	go fmt ./...
test:
	go test -count=1 ./...
vet:
	go vet ./...
check:
	bash scripts/check.sh
build:
	go build -o bin/relay ./cmd/relay
up:
	docker compose up -d --build vpn relay

deploy:
	bash scripts/deploy.sh

local-up:
	LOCAL_PLEX=1 bash scripts/deploy.sh
local-down:
	docker compose -f compose.yaml -f compose.local-plex.yaml down
smoke:
	bash scripts/smoke.sh
smoke-video:
	SMOKE_VIDEO=1 bash scripts/smoke.sh
logs:
	docker compose logs -f --tail=100 vpn relay

down:
	docker compose down
