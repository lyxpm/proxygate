FROM node:22-slim AS web
ENV NPM_CONFIG_REGISTRY=https://registry.npmmirror.com \
    COREPACK_NPM_REGISTRY=https://registry.npmmirror.com
WORKDIR /app/web
RUN corepack enable && corepack prepare pnpm@10.6.2 --activate
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile --package-import-method=copy
COPY web/ ./
RUN pnpm build

FROM golang:1.27-alpine AS build
ARG VERSION=dev
ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /app/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/proxygate/proxygate/internal/app.Version=${VERSION}" \
    -o /out/proxygate ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S -g 10001 pg \
    && adduser -S -u 10001 -G pg pg \
    && mkdir -p /data \
    && chown pg:pg /data
WORKDIR /app
COPY --from=build /out/proxygate /usr/local/bin/proxygate
USER root
ENV PG_DATA_DIR=/data \
    PG_ADMIN_LISTEN=0.0.0.0:18080
EXPOSE 80 443 443/udp 18080 18443
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:18080/api/v1/system/health >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/proxygate"]
