# Build context: this directory (examples/go/codec-server).
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN GOWORK=off go mod download
COPY *.go ./
RUN GOWORK=off CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /dex-codec-server .

# Static, shell-less image that runs as the non-root user 65532.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /dex-codec-server /dex-codec-server
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/dex-codec-server"]
