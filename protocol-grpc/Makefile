PROTOC_VERSION := 28.3
PROTOC_GEN_GO_VERSION := v1.35.2
PROTOC_GEN_GO_GRPC_VERSION := v1.5.1

PROTO_DIR := protos
GEN_DIR := gen/dexpb
GOPATH_BIN := $(shell go env GOPATH)/bin
export PATH := $(GOPATH_BIN):$(PATH)

.PHONY: proto clean ensure-tools

proto: ensure-tools
	protoc \
		-I $(PROTO_DIR) \
		--go_out=$(GEN_DIR) \
		--go_opt=paths=source_relative \
		--go-grpc_out=$(GEN_DIR) \
		--go-grpc_opt=paths=source_relative \
		$(PROTO_DIR)/dex.proto

clean:
	rm -f $(GEN_DIR)/*.go

ensure-tools:
	@which protoc > /dev/null 2>&1 || (echo "protoc not found. Install protoc $(PROTOC_VERSION)" && exit 1)
	@which protoc-gen-go > /dev/null 2>&1 || go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	@which protoc-gen-go-grpc > /dev/null 2>&1 || go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)
