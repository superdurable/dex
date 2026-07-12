# syntax=docker/dockerfile:1.7

FROM golang:1.25 AS builder

WORKDIR /workspace/server

COPY server/go.mod server/go.sum ./
COPY protocol-grpc ../protocol-grpc
COPY sdk-go ../sdk-go

RUN go mod download

COPY server/ ./

ARG TARGETOS=linux
ARG TARGETARCH

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/dex-server ./cmd/main

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=builder /out/dex-server /app/dex-server

EXPOSE 7233 7234 7946 7947 9090

ENTRYPOINT ["/app/dex-server"]
