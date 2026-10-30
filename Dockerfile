# syntax=docker/dockerfile:1
# Go cross-compiles natively; only the runtime stage runs per target platform.
FROM --platform=$BUILDPLATFORM golang:1.23-bookworm AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/ ./cmd/...

FROM python:3.12-slim-bookworm
LABEL org.opencontainers.image.source=https://github.com/aquare11e/karusel-tuner \
      org.opencontainers.image.description="Karusel web stream as an HDHomeRun tuner with XMLTV guide for Plex" \
      org.opencontainers.image.licenses=MIT
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg tini \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir 'streamlink>=8,<9'
COPY --from=builder /out/relay /out/vpnconf /usr/local/bin/
# /vpn-config is the mount point of the volume vpnconf writes the profile to;
# a new named volume takes this ownership, so the non-root user can write.
RUN useradd --create-home --uid 10001 relay \
    && mkdir /vpn-config && chown relay:relay /vpn-config
USER relay
EXPOSE 8080
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/usr/local/bin/relay"]
