# Develop Dex server

Any contribution is welcome. Even just a fix for typo in a code comment, or README/wiki.

See [Design doc](https://docs.google.com/document/d/1BpJuHf67ibaOWmN_uWw_pbrBVyb6U1PILXyzohxA5Ms/edit) for how it works.

Here is the repository layout if you are interested to learn about it:

* `cmd/` the code to bootstrap the server -- loading config and connect to Cadence/Temporal service, and start Dex API
  and interpreter service
* `config/` the config to start the server, and also config template to start the Docker image
* `docker-compose/` Compose files for server integration-test dependencies
* `gen/dexpb/` the generated protobuf/gRPC stubs from [`protos/dex.proto`](../protos/dex.proto)
* `integ/` the end to end integration tests.
    * `workflow/` the Dex workflows that are written without SDK(just implemented the REST APIs)
    * `*.go` the tests
* IDL source lives in monorepo `protos/dex.proto` (see [`docs/design/idl-renames.md`](../docs/design/idl-renames.md))
* `script/` some scripts
    * `http/` some example HTTP scripts to call server, like REST API
    * `start-server.sh` the script to start Dex server in Docker image
* `service/` Dex implementation
	* `runtime/` reusable API/interpreter bootstrap used by the server binary and `dexcli`
    * `api/` API service implementation
        * `cadence/` the Cadence abstraction of `UnifiedClient`
        * `temporal/` the Temporal abstraction of `UnifiedClient`
        * `*.go` the implementation of API service using `UnifiedClient` so that it works for both Cadence and Temporal
    * `interpreter/` interpreter worker service implementation
        * `cadence/` the Cadence abstraction of `ActivityProvider` and `WorkflowProvider`
        * `temporal/` the Temporal abstraction of `ActivityProvider` and `WorkflowProvider`
        * `*.go` the implementation of interpreter workflow service using `ActivityProvider` and `WorkflowProvider` so
          that it works for both Cadence and Temporal
            * `workflowImpl.go` the core workflow implementation
    * `common/` some common libraries between `api` and `interpreter`
    * `*.go` some common definitions between `api` and `interpreter`

## How to update IDL and the generated code

1. Edit [`protos/dex.proto`](../protos/dex.proto)
2. Server rewrite (preferred while SDKs are frozen): `make idl-code-gen-server`
   (or `make -C ../protos proto-server-go`) — refreshes only `server/gen/dexpb`.
3. Full regen (server + SDKs): `make idl-code-gen` (or `make -C ../protos proto`).

## Temporal / Cadence DataConverters

Interpreter history and query/update payloads are binary protobuf (not JSON).
Factories live in [`service/common/converter`](service/common/converter):

* `NewTemporalDataConverter()` — Proto binary before JSON escape hatch
* `NewCadenceDataConverter()` — `DEXDC` framed wire format (proto/json/raw)

API clients and interpreter workers must use the **same** factory and memo codec.
Temporal workers inherit it from their client; Cadence worker Options set it
explicitly. Indexed Attributes still use the backend-native mapper.

## Interpreter runtime constraints

Interpreter workflows and activities use constructor injection. Do not add mutable
package-global environments or registries.

`WaitForStepCompletion`, `WaitForAttribute`, and locking RPCs are Temporal-only
synchronous updates. Non-locking RPCs remain available on both backends.

Their protobuf requests require a client-generated `request_id`. The server passes
it to Temporal as the Update ID, so one logical call must reuse the same ID across
retries and keep the operation and input unchanged. Temporal deduplicates only
within one namespace, workflow ID, and run ID; Continue-as-New starts a new
deduplication scope. SDK support remains deferred during the server rewrite.

Memo is reserved for worker-target and request-id metadata. Never store attributes
in Memo.

# How to run server or integration tests

## Run the local Dex environment

For normal local development, install and run `dexcli`:

```shell
brew install superdurable/tap/dexcli
dexcli dev
```

This starts Dex Server, Dex Web, and the internal Temporal backend. See
[`../cli/README.md`](../cli/README.md) for configuration options.

## Prepare standalone server dependencies

The Compose files below start integration-test dependencies only. They do not
start Dex Server.

### Repository Cadence and Temporal dependencies

Start with shutting down already running dependencies: `docker compose -f docker-compose/integ-dependencies.yml down`

Then run `docker compose -f docker-compose/integ-dependencies.yml up` to start
both Cadence and Temporal.

### External Temporal service

First of all, you need a Temporal service if you haven't had it:

Option 1 (recommended): use [Temporal CLI](https://github.com/temporalio/cli) -- `temporal server start-dev`

Option 2: use [temporal docker-compose](https://github.com/temporalio/docker-compose)


Assuming you are using the `default` namespace, grant Dex permission to list
and add visibility indexes. Dex Server synchronizes `FlowType` and
`ActiveStepTypes` before serving; each Worker synchronizes application indexes.
The default deadline is two minutes and is configurable with
`interpreter.attributeIndexSyncTimeout`.

### External Cadence service

1. You can run a local Cadence server following the [instructions](https://github.com/uber/cadence/tree/master/docker)

```
docker-compose -f docker-compose-es-v7.yml up
```

2. Register a domain if needed: `cadence --do default domain register`.
3. Grant Dex Admin API access to add visibility indexes. Configure an optional
   security token with `interpreter.cadence.adminSecurityToken`.

Dex polls Cadence visibility after registration with exponential backoff until
the configured deadline. `KEYWORD_ARRAY` uses Cadence's multi-valued Keyword
representation and `TEXT` uses String.

## Run the standalone server

The standalone server binary supports full Temporal and Cadence YAML
configuration through the same `service/bootstrap` package.

The first step you may want to explore is to run it locally!

To run the server with Temporal
* If you are in an IDE, you can run the main function in `./cmd/main.go` with argument `start`.
* Or in terminal `go run cmd/server/main.go start`
* Or build the binary and run it by `make bins` and then run `./dex-server start`

To run with Cadence, make sure you specify the cadence config `--config config/development_cadence.yaml start`:
* In an IDE, you can run the main function in `./cmd/main.go` with argument ` --config config/development_cadence.yaml start`.
* Or in terminal `go run cmd/server/main.go --config config/development_cadence.yaml start`
* Or build the binary and run it by`make bins` and then run `./dex-server --config config/development_cadence.yaml start`

## Run the integration tests
For development, you may want to run the test locally for debugging, especially your PR has failed the tests in CI pipeline.

:warning: Cadence visibility propagation can be slow. Dex waits up to
`interpreter.attributeIndexSyncTimeout` instead of requiring a fixed sleep.

* To run the whole integ test suite against Cadence+Temporal service by this command `make integTests`
* To run the whole suite for Temporal only `make temporalIntegTests` 
* To run the whole suite for Cadence only `make cadenceIntegTests`
* To run a specify test case or a test file, you can utilize the IDE or `go test` command.

To reuse the Temporal and Dex Server managed by `dexcli dev`, run:

```shell
make temporalIntegTestsAgainstLocalDexDev
```

This target connects to Temporal at `127.0.0.1:7233` and Dex Server at
`127.0.0.1:8801`. Override them with `temporalHostPort` and
`dexServerAddress`. The tests synchronize missing custom integration indexes
through Dex Server but never start or stop Temporal or Dex Server. Each test still
starts its own worker on an ephemeral localhost port. Visibility search
assertions are disabled because local Temporal uses SQLite instead of the CI
visibility backend; indexed attribute read/write coverage remains enabled. The
external client also supplies the suite's SYNC durability default when a start
request omits it and preserves the suite's 12-second API wait cap.

The target excludes top-level tests whose names contain `ContinueAsNew` by
default:

```shell
make temporalIntegTestsAgainstLocalDexDev \
  dexServerAddress=127.0.0.1:18801 \
  temporalHostPort=127.0.0.1:7233
```

Set `skipContinueAsNewTests=false` to run every top-level test. When filtering,
the target lists the tests and runs those whose names do not contain
`ContinueAsNew`.

Tests requiring per-process Dex configuration—blob storage, memo
encryption, default headers, or disabled sticky cache—are skipped in this mode.
They remain covered by `temporalIntegTests`, which starts an in-process Dex
runtime for every test.

CI integration tests are partitioned by top-level test name. Dynamic subtests run
in the same partition as their parent. Reproduce a CI partition locally with:

```shell
make ci-temporal-integ-test totalPartitions=5 partitionNum=0
make ci-cadence-integ-test totalPartitions=5 partitionNum=0
```

`totalPartitions` defaults to `1` and `partitionNum` defaults to `0`, which runs
the complete suite. Each CI partition uses an independent runner and backend
stack.

### Measure integration coverage

Run the main Temporal and Cadence integration suite with Go coverage:

```shell
make integrationCoverage
```

The report measures production packages under `./service/...` from integration
tests only. It does not run `unitTests`. Open `coverage/index.html` for annotated
source, or inspect `coverage/coverage.txt` for per-function totals.
`coverage/coverage.out` is the profile format uploaded by CI.

Server CI also instruments the Attribute Store, Blob Store, Web API, Temporal,
and Cadence integration jobs. Each matrix partition publishes binary Go coverage
data, and the `Integration coverage` job merges every successful partition before
uploading one report to Codecov. The upload uses the `server-integration` flag
and GitHub OIDC. The merged HTML, text, and profile reports are available in the
`server-integration-coverage` Actions artifact.

To debug the failed test, search for `--- FAIL` in the output logs (in GitHub Action, click "view raw logs"") 

### Pending Step failures

Regular WaitFor and Execute activities use deterministic internal activity IDs
that contain the Step execution ID. Temporal and Cadence describe responses use
that ID to associate a pending activity's latest failure with the matching
active Step returned by `GetFlowState`. Never join by Step type: multiple active
executions may have the same type. A concurrent describe/query change is safe
only when the Step execution ID still exists in the queried active snapshot.

ASYNC local activities have no backend pending-activity record. Their retry
failure becomes observable after the interpreter falls back to a regular
activity.
