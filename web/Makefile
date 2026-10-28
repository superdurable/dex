.PHONY: assets build test

assets:
	npm ci
	npm run check
	npm run build

build: assets
	GOWORK=off go build -trimpath -o dex-web ./cmd/dex-web

test:
	GOWORK=off go test ./... $(TEST_ARGS)
