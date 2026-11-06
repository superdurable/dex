# syntax=docker/dockerfile:1.7

FROM rust:1.97.1-bookworm AS builder

WORKDIR /src
COPY LICENSE ./LICENSE
COPY s3local/ ./s3local/
WORKDIR /src/s3local
ARG TARGETARCH
RUN --mount=type=cache,id=s3local-cargo-registry-${TARGETARCH},target=/usr/local/cargo/registry \
    --mount=type=cache,id=s3local-target-${TARGETARCH},target=/src/s3local/target \
    cargo build --release --locked -p s3local-server --bin s3local \
    && mkdir -p /out \
    && cp target/release/s3local /out/s3local

FROM scratch AS binary
COPY --from=builder /out/s3local /s3local

FROM debian:bookworm-slim

RUN groupadd --system --gid 10001 s3local \
    && useradd --system --uid 10001 --gid 10001 --home-dir /nonexistent --shell /usr/sbin/nologin s3local \
    && mkdir -p /var/lib/s3local \
    && chown 10001:10001 /var/lib/s3local

COPY --from=builder /out/s3local /usr/local/bin/s3local
COPY LICENSE /licenses/LICENSE

ENV S3LOCAL_LISTEN_ADDRESS=0.0.0.0:9000 \
    S3LOCAL_DATA_DIRECTORY=/var/lib/s3local

USER 10001:10001
VOLUME ["/var/lib/s3local"]
EXPOSE 9000
ENTRYPOINT ["/usr/local/bin/s3local"]
