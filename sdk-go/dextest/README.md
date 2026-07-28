# Unit tests APIs for Dex

The APIs are generated using [mockgen](https://github.com/uber-go/mock) by the below commands:

```shell
  mockgen -source=dex/persistence.go -package=dextest -destination=dextest/persistence.go
  mockgen -source=dex/communication.go -package=dextest -destination=dextest/communication.go
  mockgen -source=dex/workflow_context.go -package=dextest -destination=dextest/workflow_context.go
  mockgen -source=dex/client.go -package=dextest -destination=dextest/client.go
```

or running this on sdk root folder

```shell
go generate ./...
```

## Usage

See the [example](./example) for more details.
