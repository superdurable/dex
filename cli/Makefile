.PHONY: build test integration-test

TEST_ARGS ?=

build:
	cd ../web && npm ci && npm run check && npm run build
	GOWORK=off go build -trimpath -o dexcli ./cmd/dexcli

test:
	GOWORK=off go test ./... $(TEST_ARGS)

integration-test:
	GOWORK=off go test -tags=integration ./internal/... -count=1 -v $(TEST_ARGS)

.PHONY: search-projection-e2e
search-projection-e2e:
	GOWORK=off go build -trimpath -o dexcli ./cmd/dexcli
	GOWORK=off go test -tags=search_projection_e2e ./integ/searchprojection -count=1 -v
