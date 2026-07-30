# Develop Dex Golang SDK

## Repo layout

Any contribution is welcome. Even just a fix for typo in a code comment, or README/wiki.

Here is the repository layout if you are interested to learn about it:

* `gen/dexpb/` the generated protobuf/gRPC stubs from [`protos/dex.proto`](../protos/dex.proto)
* IDL source lives in monorepo `protos/dex.proto` (see [`docs/design/idl-renames.md`](../docs/design/idl-renames.md))
* `dex` the main directory
  * `blobcache/` the independently tested disk blob cache
  * root `.go` files contain public contracts and Phase 2 internal mappers
  * `contracts_test.go` compiles the API from an external application package
  * package-internal tests cover value, protobuf, error, and hydration mapping
* `examples/` contains compilable authoring examples
* `integ/` is retained for migration when the runtime and client phases land

Application packages must import `dex`, not `gen/dexpb`.

## Phase 2 verification

Run codec, mapper, contract, example, and cache tests through the Makefile:

```text
make unitTests 2>&1 | tee /tmp/test-go-sdk-phase2.log
make blobCacheTests 2>&1 | tee /tmp/test-go-sdk-phase2-blobcache.log
make copyright-check 2>&1 | tee /tmp/test-go-sdk-phase2-copyright.log
```

Phase 2 does not run the legacy integration suite because registration, worker,
and transport are outside this phase.

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
