FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/foya ./cmd/foya

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        bubblewrap \
        ca-certificates \
        curl \
        git \
        nodejs \
        npm \
        openssh-client \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 foya \
    && useradd --uid 10001 --gid foya --create-home --home-dir /home/foya \
        --shell /usr/sbin/nologin foya \
    && install -d -o foya -g foya -m 0700 /var/lib/foya /workspace

COPY --from=build /out/foya /usr/local/bin/foya

USER foya
WORKDIR /workspace
EXPOSE 8787
VOLUME ["/var/lib/foya", "/workspace"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD curl --fail --silent http://127.0.0.1:8787/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/foya", "serve"]
CMD ["--listen", "0.0.0.0:8787", "--data-dir", "/var/lib/foya", "--auth-token-hash-file", "/run/secrets/foya_auth_token_sha256", "--allow-plaintext"]
