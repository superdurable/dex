module github.com/superdurable/dex/cli

go 1.26.0

require (
	github.com/google/uuid v1.6.0
	github.com/stretchr/testify v1.11.1
	github.com/superdurable/dex/sdk-go v0.0.0
	github.com/superdurable/dex/web v0.0.0
	golang.org/x/tools v0.49.0
	google.golang.org/grpc v1.82.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	golang.org/x/mod v0.40.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260414002931-afd174a4e478 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/superdurable/dex/sdk-go => ../sdk-go

replace github.com/superdurable/dex/web => ../web
