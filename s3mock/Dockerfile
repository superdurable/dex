# syntax=docker/dockerfile:1.7

FROM rust:1.97.1-bookworm AS builder

WORKDIR /src
COPY LICENSE ./LICENSE
COPY s3mock/ ./s3mock/
WORKDIR /src/s3mock
ARG TARGETARCH
RUN --mount=type=cache,id=s3mock-cargo-registry-${TARGETARCH},target=/usr/local/cargo/registry \
    --mount=type=cache,id=s3mock-target-${TARGETARCH},target=/src/s3mock/target \
    cargo build --release --locked -p s3mock-server --bin s3mock \
    && mkdir -p /out \
    && cp target/release/s3mock /out/s3mock

FROM scratch AS binary
COPY --from=builder /out/s3mock /s3mock

FROM debian:bookworm-slim

RUN groupadd --system --gid 10001 s3mock \
    && useradd --system --uid 10001 --gid 10001 --home-dir /nonexistent --shell /usr/sbin/nologin s3mock \
    && mkdir -p /var/lib/s3mock \
    && chown 10001:10001 /var/lib/s3mock

COPY --from=builder /out/s3mock /usr/local/bin/s3mock
COPY LICENSE /licenses/LICENSE

ENV S3MOCK_LISTEN_ADDRESS=0.0.0.0:9000 \
    S3MOCK_DATA_DIRECTORY=/var/lib/s3mock

USER 10001:10001
VOLUME ["/var/lib/s3mock"]
EXPOSE 9000
ENTRYPOINT ["/usr/local/bin/s3mock"]
