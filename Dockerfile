FROM --platform=$BUILDPLATFORM oven/bun:1.4.3@sha256:ec06c3b6cea04192ae6770c434f668ca41d343ad19fa6472216c7b48be39c598 AS web
WORKDIR /src/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run build

FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine@sha256:85dc1069ac644ea3c527b177303a406eb3358192816cd7f9e5848eb658851673 AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY --from=web /src/internal/server/ui ./internal/server/ui
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /tunwatch ./cmd/tunwatch \
    && mkdir -p /data && chown 0:65532 /data && chmod 0770 /data

FROM scratch
LABEL org.opencontainers.image.source="https://github.com/yude/tunwatch" \
      org.opencontainers.image.description="Linux L2 network presence monitoring"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build --chown=0:65532 /data /data
COPY --from=build /tunwatch /tunwatch
USER 0:65532
EXPOSE 8080
ENTRYPOINT ["/tunwatch"]
CMD ["-config", "/etc/tunwatch/config.json"]
