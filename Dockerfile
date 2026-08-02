FROM node:24-bookworm-slim AS dashboard
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

FROM golang:1.25.1-bookworm AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=dashboard /src/web/dist/ /src/web/dist/
ARG VERSION=dev
ARG COMMIT=none
RUN CGO_ENABLED=0 go test ./... && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/Nciae-Zyh/stundeck/internal/version.Version=${VERSION} -X github.com/Nciae-Zyh/stundeck/internal/version.Commit=${COMMIT}" -o /out/stundeck ./cmd/stundeck && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/stundeck-notify ./cmd/stundeck-notify

FROM debian:bookworm-slim AS natmap
ARG TARGETARCH
ARG NATMAP_VERSION=20260214
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl && rm -rf /var/lib/apt/lists/*
RUN set -eu; \
    case "${TARGETARCH}" in \
      amd64) asset="natmap-linux-x86_64"; checksum="f87dac13a693470a9ced03bed8c20b881ee9a56d3f8b935ca1219c694d121905" ;; \
      arm64) asset="natmap-linux-arm64"; checksum="dc0fb55bde205321a4ef704e381489e8fc5214008989d3133fb285ad39ca468e" ;; \
      *) echo "Unsupported architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL --retry 3 "https://github.com/heiher/natmap/releases/download/${NATMAP_VERSION}/${asset}" -o /natmap; \
    echo "${checksum}  /natmap" | sha256sum -c -; \
    chmod 0755 /natmap

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && \
    groupadd --system --gid 10001 stundeck && \
    useradd --system --uid 10001 --gid 10001 --home-dir /var/lib/stundeck --shell /usr/sbin/nologin stundeck && \
    install -d -o stundeck -g stundeck -m 0700 /var/lib/stundeck && \
    rm -rf /var/lib/apt/lists/*
COPY --from=backend /out/stundeck /usr/local/bin/stundeck
COPY --from=backend /out/stundeck-notify /usr/local/bin/stundeck-notify
COPY --from=natmap /natmap /usr/local/bin/natmap
COPY LICENSE /usr/share/licenses/stundeck/LICENSE
COPY third_party/NATMap-LICENSE /usr/share/licenses/natmap/LICENSE

ENV STUNDECK_LISTEN=0.0.0.0:8080 \
    STUNDECK_DATA_DIR=/var/lib/stundeck \
    STUNDECK_NATMAP_BINARY=/usr/local/bin/natmap \
    STUNDECK_NOTIFY_BINARY=/usr/local/bin/stundeck-notify

VOLUME ["/var/lib/stundeck"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["stundeck", "healthcheck"]
USER 10001:10001
ENTRYPOINT ["stundeck"]
