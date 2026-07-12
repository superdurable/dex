# syntax=docker/dockerfile:1.7

FROM golang:1.25 AS builder

WORKDIR /workspace/benchmark

COPY benchmark/go.mod benchmark/go.sum* ./
COPY protocol-grpc ../protocol-grpc
COPY sdk-go ../sdk-go
COPY server ../server

RUN go mod download

COPY benchmark/ ./

ARG TARGETOS=linux
ARG TARGETARCH

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/dex-benchmark-worker ./cmd/benchmarkworker

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=builder /out/dex-benchmark-worker /app/dex-benchmark-worker

EXPOSE 9123

ENTRYPOINT ["/app/dex-benchmark-worker"]
