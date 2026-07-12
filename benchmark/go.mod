module github.com/superdurable/dex/benchmark

go 1.25.0

require (
	github.com/stretchr/testify v1.11.1
	github.com/superdurable/dex/protocol-grpc v0.0.0-20260413205803-fab56b4e62d1
	github.com/superdurable/dex/sdk-go v0.0.0-00010101000000-000000000000
	google.golang.org/grpc v1.80.0
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.35.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260401024825-9d38bb4040a9 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/superdurable/dex/protocol-grpc => ../protocol-grpc
	github.com/superdurable/dex/sdk-go => ../sdk-go
)
