FROM node:22-bookworm AS web-builder

WORKDIR /src
COPY web/package.json web/package-lock.json web/
COPY packages/flow-definition-renderer/package.json packages/flow-definition-renderer/
RUN cd web && npm ci
COPY web/ web/
COPY packages/flow-definition-renderer/ packages/flow-definition-renderer/
RUN cd web && npm run build

FROM golang:latest AS server-builder

WORKDIR /src
COPY server/ server/
COPY web/ web/
COPY --from=web-builder /src/web/assets/dist/ web/assets/dist/
RUN cd server/cmd/server && CGO_ENABLED=0 go build -trimpath -o /dex-server .

FROM debian:bookworm-slim

ENV CONFIG_TEMPLATE_PATH=''
ENV SRC_ROOT=''
ENV BACKEND_DEPENDENCY='temporal'
ENV TEMPORAL_SERVICE_NAME=''
ENV CADENCE_SERVICE_NAME=''

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y netcat-traditional \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

COPY --from=server-builder /dex-server /dex/dex-server
COPY server/config/ /dex/config/
COPY server/script/start-server.sh /dex/script/start-server.sh
COPY server/LICENSE.md server/LEGACY_NOTICES.md /dex/
COPY server/LICENSES/ /dex/LICENSES/

RUN chmod +x /dex/script/start-server.sh

WORKDIR /dex
EXPOSE 8801 8802
ENTRYPOINT ["/dex/script/start-server.sh"]
