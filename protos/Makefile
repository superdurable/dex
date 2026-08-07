PROTOC_VERSION := 35.1
PROTOC_GEN_GO_VERSION := v1.36.11
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2
PROTOC_GEN_GRPC_JAVA_VERSION := 1.69.1

PROTO_DIR := .
PROTO_FILE := $(PROTO_DIR)/dex.proto

SERVER_GO_OUT := ../server/gen/dexpb
SDK_GO_OUT := ../sdk-go/gen/dexpb
SDK_JAVA_OUT := ../sdk-java/src/main/java
SDK_PYTHON_OUT := ../sdk-python/dex/dexpb

SERVER_GO_PKG := github.com/superdurable/dex/gen/dexpb
SDK_GO_PKG := github.com/superdurable/dex/sdk-go/gen/dexpb

GOPATH_BIN := $(shell go env GOPATH)/bin
export PATH := $(GOPATH_BIN):$(PATH)

TOOLS_DIR := .tools
GRPC_JAVA_PLUGIN := $(TOOLS_DIR)/protoc-gen-grpc-java
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)
ifeq ($(UNAME_S),Darwin)
  ifeq ($(UNAME_M),arm64)
    GRPC_JAVA_CLASSIFIER := osx-aarch_64
  else
    GRPC_JAVA_CLASSIFIER := osx-x86_64
  endif
else ifeq ($(UNAME_S),Linux)
  ifeq ($(UNAME_M),aarch64)
    GRPC_JAVA_CLASSIFIER := linux-aarch_64
  else
    GRPC_JAVA_CLASSIFIER := linux-x86_64
  endif
else
  GRPC_JAVA_CLASSIFIER := windows-x86_64
endif

.PHONY: proto proto-server-go proto-sdk-go proto-python clean ensure-tools ensure-python-tools

proto: proto-server-go

# Server-only codegen for the server rewrite. Does not touch SDK trees.
proto-server-go: ensure-tools
	rm -rf $(SERVER_GO_OUT)
	mkdir -p $(SERVER_GO_OUT)
	protoc \
		-I $(PROTO_DIR) \
		--go_out=$(SERVER_GO_OUT) \
		--go_opt=paths=source_relative \
		--go_opt=Mdex.proto=$(SERVER_GO_PKG) \
		--go-grpc_out=$(SERVER_GO_OUT) \
		--go-grpc_opt=paths=source_relative \
		--go-grpc_opt=Mdex.proto=$(SERVER_GO_PKG) \
		$(PROTO_FILE)

proto-sdk-go: ensure-tools
	rm -rf $(SDK_GO_OUT)
	mkdir -p $(SDK_GO_OUT)
	protoc \
		-I $(PROTO_DIR) \
		--go_out=$(SDK_GO_OUT) \
		--go_opt=paths=source_relative \
		--go_opt=Mdex.proto=$(SDK_GO_PKG) \
		--go-grpc_out=$(SDK_GO_OUT) \
		--go-grpc_opt=paths=source_relative \
		--go-grpc_opt=Mdex.proto=$(SDK_GO_PKG) \
		$(PROTO_FILE)

# proto-java: ensure-tools $(GRPC_JAVA_PLUGIN)
# 	rm -rf $(SDK_JAVA_OUT)/io/superdurable/gen
# 	mkdir -p $(SDK_JAVA_OUT)
# 	protoc \
# 		-I $(PROTO_DIR) \
# 		--java_out=$(SDK_JAVA_OUT) \
# 		--plugin=protoc-gen-grpc-java=$(GRPC_JAVA_PLUGIN) \
# 		--grpc-java_out=$(SDK_JAVA_OUT) \
# 		$(PROTO_FILE)
#
proto-python: ensure-python-tools
	rm -rf $(SDK_PYTHON_OUT)
	mkdir -p $(SDK_PYTHON_OUT)
	uv run --project ../sdk-python --frozen python -m grpc_tools.protoc \
		-I $(PROTO_DIR) \
		--python_out=$(SDK_PYTHON_OUT) \
		--grpc_python_out=$(SDK_PYTHON_OUT) \
		--pyi_out=$(SDK_PYTHON_OUT) \
		$(PROTO_FILE)
	printf '%s\n' '# Generated package for Dex protobuf stubs.' > $(SDK_PYTHON_OUT)/__init__.py
	# grpc stubs import "dex_pb2"; fix to relative for package layout.
	@if [ -f $(SDK_PYTHON_OUT)/dex_pb2_grpc.py ]; then \
		sed -i.bak 's/^import dex_pb2 as/from . import dex_pb2 as/' $(SDK_PYTHON_OUT)/dex_pb2_grpc.py && rm -f $(SDK_PYTHON_OUT)/dex_pb2_grpc.py.bak; \
	fi

clean:
	rm -rf $(SERVER_GO_OUT) $(SDK_GO_OUT)
	rm -rf $(SDK_JAVA_OUT)/io/superdurable/gen
	rm -rf $(SDK_PYTHON_OUT)

ensure-tools:
	@which protoc > /dev/null 2>&1 || (echo "protoc not found. Install protoc $(PROTOC_VERSION)" && exit 1)
	@which protoc-gen-go > /dev/null 2>&1 || go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	@which protoc-gen-go-grpc > /dev/null 2>&1 || go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

ensure-python-tools:
	@which uv > /dev/null 2>&1 || (echo "uv not found. Install uv 0.12.2" && exit 1)

$(GRPC_JAVA_PLUGIN):
	mkdir -p $(TOOLS_DIR)
	curl -fsSL -o $(GRPC_JAVA_PLUGIN) \
		"https://repo1.maven.org/maven2/io/grpc/protoc-gen-grpc-java/$(PROTOC_GEN_GRPC_JAVA_VERSION)/protoc-gen-grpc-java-$(PROTOC_GEN_GRPC_JAVA_VERSION)-$(GRPC_JAVA_CLASSIFIER).exe"
	chmod +x $(GRPC_JAVA_PLUGIN)
