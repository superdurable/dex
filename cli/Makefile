.PHONY: build test integration-test

build:
	cd ../web && npm ci && npm run check && npm run build
	GOWORK=off go build -trimpath -o dexcli ./cmd/dexcli

test:
	GOWORK=off go test ./...

integration-test:
	GOWORK=off go test -tags=integration ./internal/... -count=1 -v
