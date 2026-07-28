# Develop Dex Golang SDK

## Repo layout

Any contribution is welcome. Even just a fix for typo in a code comment, or README/wiki.

Here is the repository layout if you are interested to learn about it:

* `gen/dexpb/` the generated protobuf/gRPC stubs from [`protos/dex.proto`](../protos/dex.proto)
* `integ/` the end to end integration tests.
    * `init.go` the initiation & registration of workflows. It's using global variables just for convenience
    * `main_test` the setup + tear down for running local in-memory Dex worker with GoSDK
    * `xyz_test` the test for a test case xyz
    * `xyz_workflow.go` the test workflow for a test xyz
    * `xyz_workflow_state_*` the test workflow states for a test xyz
* IDL source lives in monorepo `protos/dex.proto` (see [`docs/design/idl-renames.md`](../docs/design/idl-renames.md))
* `dex` the main directory
  * `*_impl.go` these are implementation for SDK. Ideally we should put them in separate folder, but Golang doesn't allow circular dependency, and we hate to use alias across packages
  * `internal_*.go` these are implementation for SDK
  * `_test.go` the unit test
  * other `.go` the interfaces defined in this SDK for user to use

## How to update IDL and the generated code
1. Edit [`protos/dex.proto`](../protos/dex.proto)
2. Run `make idl-code-gen` (or `make -C ../protos proto`) to refresh stubs in server + SDKs

### Coding convention 
There are lots of convention that we love here that we haven't summarized all of them. So you may get some code review feedback about more than just below:
* The private struct shouldn't let other structs to access their private fields. Since all the impls are in the same package, it's possible to write the code with the random access, but it would be a nightmare to maintain. We recommend to always expose a method (like `getter`) for external code to use