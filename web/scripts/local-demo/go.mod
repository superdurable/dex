module github.com/superdurable/dex/web/scripts/local-demo

go 1.26.0

require (
	github.com/superdurable/dex/agents/go/core v0.0.0
	github.com/superdurable/dex/blob-cache-go v0.1.2
	github.com/superdurable/dex/sdk-go v1.45.1-0.20261112014933-d42ec7ae6850
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgraph-io/ristretto/v2 v2.4.2 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251202230838-ff82c1b0f217 // indirect
	google.golang.org/grpc v1.79.3 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/superdurable/dex/sdk-go => ../../../sdk-go

replace github.com/superdurable/dex/blob-cache-go => ../../../blob-cache-go

replace github.com/superdurable/dex/agents/go/core => ../../../agents/go/core
