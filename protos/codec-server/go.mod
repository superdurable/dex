module github.com/superdurable/dex/protos/codec-server

go 1.26.0

replace github.com/superdurable/dex => ../../server

replace go.temporal.io/sdk => github.com/superdurable/temporal-sdk-go v1.47.1-superdurable.2

require (
	github.com/stretchr/testify v1.11.1
	github.com/superdurable/dex v0.0.0
	go.temporal.io/api v1.63.4
	go.temporal.io/sdk v1.47.1-superdurable.2
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.27.2 // indirect
	github.com/nexus-rpc/nexus-proto-annotations v0.1.0 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260414002931-afd174a4e478 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260414002931-afd174a4e478 // indirect
	google.golang.org/grpc v1.82.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
