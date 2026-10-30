# syntax=docker/dockerfile:1
FROM golang:1.23-bookworm AS builder
WORKDIR /src
COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/relay ./cmd/relay

FROM python:3.12-slim-bookworm
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg tini \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir 'streamlink>=8,<9'
COPY --from=builder /out/relay /usr/local/bin/relay
RUN useradd --create-home --uid 10001 relay
USER relay
EXPOSE 8080
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/usr/local/bin/relay"]
