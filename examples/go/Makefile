MAKEFLAGS += --no-builtin-rules
.SUFFIXES:

.PHONY: bins clean datasetDealDemo e2eTests fmt tests triggerDatasetDealDemo unitTests

default: help

bins:
	GOWORK=off go build -o dex-samples ./cmd/server

clean:
	rm -f dex-samples

datasetDealDemo:
	./run-dataset-deal-demo.sh

triggerDatasetDealDemo:
	./trigger-dataset-deal-demo.sh

e2eTests:
	./run-e2e-tests.sh

fmt:
	gofmt -s -w ./cmd ./integ ./workflows

tests: unitTests

unitTests:
	GOWORK=off go test -v ./cmd/... ./workflows/...

help:
	@printf "\033[36m%-20s\033[0m %s\n" 'help' 'Prints this help message'
	@grep -E "^[a-zA-Z_-]+:.*" $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":"}; {printf "\033[36m%-20s\033[0m\n", $$1}' | sort -u
