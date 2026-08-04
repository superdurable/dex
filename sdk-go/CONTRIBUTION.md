# Develop Dex Golang SDK

## Repo layout

Any contribution is welcome. Even just a fix for typo in a code comment, or README/wiki.

Here is the repository layout if you are interested to learn about it:

* `gen/dexpb/` the generated protobuf/gRPC stubs from [`protos/dex.proto`](../protos/dex.proto)
* IDL source lives in monorepo `protos/dex.proto` (see [`docs/design/idl-renames.md`](../docs/design/idl-renames.md))
* `dex` the main directory
  * `blobcache/` the independently tested disk blob cache
  * root `.go` files contain public contracts and private registration/runtime
  * `contracts_test.go` compiles the API from an external application package
  * package-internal tests cover registration, value, protobuf, errors, and
    hydration
* `examples/` contains compilable authoring examples
* `integ/` contains the Temporal-backed public Client end-to-end suite

Application packages must import `dex`, not `gen/dexpb`.

## Phase 5 verification

Run registry, codec, Client and Worker transport, examples, cache, and E2E tests
through the Makefile:

```text
make unitTests 2>&1 | tee /tmp/test-go-sdk-phase5.log
make clientIntegTests 2>&1 | tee /tmp/test-go-sdk-phase5-client.log
make workerIntegTests 2>&1 | tee /tmp/test-go-sdk-phase5-worker.log
make blobCacheTests 2>&1 | tee /tmp/test-go-sdk-phase5-blobcache.log
make temporalIntegTests 2>&1 | tee /tmp/test-go-sdk-phase5-temporal.log
make copyright-check 2>&1 | tee /tmp/test-go-sdk-phase5-copyright.log
```

`clientIntegTests` and `workerIntegTests` use real in-process gRPC with the race
detector. `temporalIntegTests` builds `dexcli`, starts its local Dex and Temporal
environment, migrates the former iWF SDK scenarios, and owns cleanup.

## How to update IDL and the generated code
1. Edit [`protos/dex.proto`](../protos/dex.proto)
2. Run `make idl-code-gen` (or `make -C ../protos proto`) to refresh stubs in server + SDKs

## Blob cache tests

Run the disk-cache component and race suite through the Makefile:

```text
make blobCacheTests
```

The suite uses temporary directories and constructor-injected filesystem fault
seams. Do not replace race or failure-path coverage with sleeps.

### Coding convention 
There are lots of convention that we love here that we haven't summarized all of them. So you may get some code review feedback about more than just below:
* The private struct shouldn't let other structs to access their private fields. Since all the impls are in the same package, it's possible to write the code with the random access, but it would be a nightmare to maintain. We recommend to always expose a method (like `getter`) for external code to use
