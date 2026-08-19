MAKEFLAGS += --no-builtin-rules
.SUFFIXES:

.PHONY: bins clean datasetDealDemo e2eTests fmt tests triggerDatasetDealDemo unitTests

default: help

bins:
	go build -o dex-samples ./cmd/server
	go build -o dex-dataset-deal ./cmd/dataset-deal

clean:
	rm -f dex-samples dex-dataset-deal

datasetDealDemo:
	./run-dataset-deal-demo.sh

triggerDatasetDealDemo:
	./trigger-dataset-deal-demo.sh

e2eTests:
	./run-e2e-tests.sh

fmt:
	gofmt -s -w ./cmd ./integ ./products ./patterns ./registry ./server ./shared

tests: unitTests

unitTests:
	go test -v ./cmd/... ./products/... ./patterns/... ./registry/...

help:
	@printf "\033[36m%-20s\033[0m %s\n" 'help' 'Prints this help message'
	@grep -E "^[a-zA-Z_-]+:.*" $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":"}; {printf "\033[36m%-20s\033[0m\n", $$1}' | sort -u
