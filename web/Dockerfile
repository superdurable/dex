FROM node:22-bookworm AS web-assets
WORKDIR /src
COPY web/package.json web/package-lock.json web/
COPY packages/flow-definition-renderer/package.json packages/flow-definition-renderer/
RUN cd web && npm ci
COPY web/ web/
COPY packages/flow-definition-renderer/ packages/flow-definition-renderer/
RUN cd web && npm run build

FROM golang:1.26 AS web-binary
ARG WEB_VERSION=dev
WORKDIR /src
COPY sdk-go/ sdk-go/
COPY web/ web/
COPY --from=web-assets /src/web/assets/dist/ web/assets/dist/
RUN cd web && GOWORK=off CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${WEB_VERSION}" -o /dex-web ./cmd/dex-web

FROM debian:bookworm-slim
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /dex
COPY --from=web-binary /dex-web /dex/dex-web
COPY web/LICENSE web/LEGACY_NOTICES.md /dex/
COPY web/LICENSES/ /dex/LICENSES/
ENV DEX_WEB_BIND_ADDRESS=0.0.0.0
EXPOSE 8802
USER 65532:65532
ENTRYPOINT ["/dex/dex-web"]
