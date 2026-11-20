MAKEFLAGS += --no-builtin-rules
.SUFFIXES:

# Build against this module's own sdk-go requirement, not the repository go.work.
export GOWORK := off

.PHONY: bins clean dockerImage fmt tests vet

default: bins

bins:
	go build -o dex-codec-server .

clean:
	rm -f dex-codec-server

dockerImage:
	docker build -t dex-codec-server .

fmt:
	gofmt -s -w .

tests:
	go test -race -count=1 ./...

vet:
	go vet ./...
