# Dex standalone cleanup verification — 2026-10-03

This receipt records the cleanup in the containing commit on `codex/dex-cleanup`.
The branch started from fetched `origin/main`, commit
`44aed939c233af1477081e8a63576a5ea211605e`, in `/private/tmp/dex-cleanup`.
The main checkout and the existing Claude cleanup worktree were both clean at
that same commit after validation. No branch, PR, release, or image was published.
Superverse source, dependency pins, images, deployments, state, and data were
outside the change.

The cleanup removes the hosted Web paths listed in the approved scope. It adds
no application Flow, Step, RPC, primitive, database, or migration. Local Web Start
still uses the submitted FlowID, the generated start schema and Step phase, a new
UUID RequestID per HTTP submission, and the Server's default DISALLOW_REUSE.
FDG Connector metadata and configuration types remain unchanged.

## Existing regression and static checks

Commands below ran from the cleanup worktree. Pipelines used `set -e -o pipefail`.
The existing Web Go and frontend suites include scripted clients and local
provider substitutes. They are regression coverage, not real-provider acceptance.
No new unit tests were added.

| Command | Observed result | Local log |
| --- | --- | --- |
| `cd web && npm ci && npm run check && npm run build` | Passed; 37 frontend test files, 315 tests; production assets built | `/private/tmp/dex-cleanup-web-check.log`, `/private/tmp/dex-cleanup-web-build.log` |
| `cd web && GOWORK=off go vet ./... && GOWORK=off go test ./...` | Passed | `/private/tmp/dex-cleanup-web-vet.log`, `/private/tmp/dex-cleanup-web-go-tests.log` |
| `make -C server unitTests` | Passed | `/private/tmp/dex-cleanup-server-unit.log` |
| `make -C server serverCommandIntegTests` | Passed; existing Web-only/late-upstream/config/selection scenarios | `/private/tmp/dex-cleanup-server-cmd.log` |
| `cd cli && GOWORK=off go test ./...` | Passed | `/private/tmp/dex-cleanup-cli-tests.log` |
| `cd cli && GOWORK=off go test -tags=integration ./internal/command -run TestVisualize -count=1` | Passed | `/private/tmp/dex-cleanup-visualize-tests.log` |
| `cd cli && GOWORK=off go test -tags=integration ./internal/dev -count=1` | Passed; includes real local Temporal/Dex startup, CLI calls and owned port shutdown | `/private/tmp/dex-cleanup-dev-tests.log` |
| `cd docs && npm ci && npm run check` | Passed both locales; SEO/route audit passed for 170 indexable routes and 7 redirects | `/private/tmp/dex-cleanup-docs-check.log` |
| `make docs-prose-check` | Passed | Normal hook also passed |
| `make copyright && make copyright-check` | Passed | `/private/tmp/dex-cleanup-copyright-check.log` |
| `make release-tooling-test` | Passed, 12 existing checks | Terminal output |
| `git diff --check` and repository branch/prose hooks | Passed | Terminal output |

`GOWORK=off go mod tidy` ran in `web`, `server/cmd/server`, then `cli`.
All three dropped the Connector SDK dependency. Web also dropped its AWS SDK and
unused test assertion dependencies. The server command retains S3 as an indirect
dependency because generic BlobStore still consumes it; no version upgrades were
introduced. Generic S3 construction still uses `CreateS3Client`, including the
existing default AWS credential chain.

The existing test cleanup also removes the reserved-namespace MinIO subtest.
Source/config/product-doc/release-tool searches for removed hosted names found
only the deliberate negative assertion in `root_mount_integration_test.go`.
Forwarded, embedded, and CSRF header names in that integration test are inputs
whose lack of influence is asserted, not surviving authority.

## Real dependency checks

`make -C server blobStoreIntegTests` passed against the actual local MinIO S3 API
at `localhost:9000`. It exercised writes, reads, enumeration, deletion, deterministic
identity, value round trips and the retained S3 Attribute cache. The test's expected
missing-object errors were observed and its assertions passed. No required service
was replaced by a stub and no missing-service skip was used.
Log: `/private/tmp/dex-cleanup-blobstore.log`.

The replacement root/header regression ran with:

```bash
cd web
DEX_WEB_ROOT_MOUNT_TEST_GRPC_ADDRESS=127.0.0.1:21801 GOWORK=off \
  go test -tags=integration . -run '^TestRootMountIgnoresForwardedHeadersWithRealDex$' -count=1 -v
```

It passed through a real Web listener with the production embedded assets and
the real Kind Dex FlowService. It checked root/v1/v2/Work Queue/Connectors/Debug
SPA responses and a JavaScript asset, ignored forwarded/embedding/CSRF/redirect
headers, root asset paths, absent bootstrap injection, HTML `no-store`,
`frame-ancestors 'none'`, `X-Frame-Options: DENY`, definition API HTTP 200 and a
readiness search through the actual Dex API.
Log: `/private/tmp/dex-cleanup-root-mount.log`.
This test fails when its required real-server address is absent.

The actual server image was also run with `--network none` and a YAML file
containing all nine removed Web keys. Strict KnownFields rejected every key
before startup, confirming the breaking configuration change.
Log: `/private/tmp/dex-cleanup-validation/removed-web-keys.log`.

## Isolated Local Kind execution and UI

A separate `dex-cleanup` cluster used a private kubeconfig at
`/private/tmp/dex-cleanup-kubeconfig`. Existing `superverse-v2` and `sv-np-136`
clusters were preserved. The new cluster ran its own PostgreSQL 13, Temporal
1.25, current Dex build, and a real Go SDK Worker. The Worker registered the
unchanged `sdk-go/integ/webv2approval/workflow.go` Flow; it has no application
provider dependency to substitute. The temporary launcher, Kubernetes manifests,
FDG and HTTP validation script are in `/private/tmp/dex-cleanup-validation`.

The current CLI generated the approval FDG with:

```bash
dexcli visualize sdk-go/integ/webv2approval/workflow.go \
  --schema-version 2.0 --json --out /private/tmp/dex-cleanup-validation/definitions/approval
```

The linux/arm64 Server used `CGO_ENABLED=0 GOWORK=off go build -trimpath` with
`-X github.com/superdurable/dex/service.DexServerVersion=cleanup-44aed939c`.
The local test image contained that binary, the real SDK Worker launcher, and
the generated approval FDG. It was loaded only into the isolated Kind cluster.

Final build, deployment, and HTTP validation commands included:

```bash
# From server/cmd/server:
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOWORK=off go build -trimpath \
  -ldflags '-X github.com/superdurable/dex/service.DexServerVersion=cleanup-44aed939c' \
  -o /private/tmp/dex-cleanup-validation/dex-server .
# From cli:
GOWORK=off go build -trimpath -o /private/tmp/dex-cleanup-validation/dexcli ./cmd/dexcli
docker build -t dex-cleanup:final /private/tmp/dex-cleanup-validation
docker save dex-cleanup:final | docker exec --privileged -i dex-cleanup-control-plane \
  ctr --namespace=k8s.io images import --platform linux/arm64 -
kubectl --kubeconfig /private/tmp/dex-cleanup-kubeconfig apply \
  -f /private/tmp/dex-cleanup-validation/application.yaml
kubectl --kubeconfig /private/tmp/dex-cleanup-kubeconfig -n dex-cleanup \
  rollout restart deployment/dex deployment/approval-worker
kubectl --kubeconfig /private/tmp/dex-cleanup-kubeconfig -n dex-cleanup \
  rollout status deployment/dex --timeout=120s
kubectl --kubeconfig /private/tmp/dex-cleanup-kubeconfig -n dex-cleanup \
  rollout status deployment/approval-worker --timeout=120s
kubectl --kubeconfig /private/tmp/dex-cleanup-kubeconfig -n dex-cleanup \
  port-forward --address 127.0.0.1 service/dex 21801:8801 21802:8802
python3 /private/tmp/dex-cleanup-validation/verify-http.py
dexcli version check --server 127.0.0.1:21801
```

| Artifact | Observed identity |
| --- | --- |
| Kind node | `kindest/node@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5` |
| Local image | `dex-cleanup:final`, manifest list `sha256:1fe4e4f444d77090610d0890edad30faa188816198f024a5bbe80235e8c7c788` |
| Dex/Worker container image ID | `sha256:16e147468ab043872ccc4a4fc18238c6d819f9f81b440286a4d8bb2e276ae8e0` |
| Server binary SHA-256 | `9b3a0973c9f27a8fbe6eaa32bb8ebba2ee051a9c3b52dfe065448ade52f0d1b2` |
| CLI binary SHA-256 | `4c86dcbc34d71ad25508459aa4e4042bf887f95d57520a039aaa180d4323584a` |
| Worker binary SHA-256 | `30252df2571dd746d7faf0f604836ba1cb5f5910ed906de63829aae59649430c` |
| Approval FDG SHA-256 | `bc2b11b216a312fba9dbe9270c10fc91134e7cb5ac9150ab9eb048a29a4671d6` |
| Diagnostic address | `127.0.0.1:21801`, forwarded to isolated Dex `:8801` |
| Versions/protocol | CLI `dev` negotiated protocol 1; SDK `dev` uses protocol 2; Server `cleanup-44aed939c` supports protocols 1–2 |

Through the actual HTTP BFF, validation observed successful Worker port health,
Start with FlowID and RunID, duplicate FlowID HTTP 409, stale definition HTTP 409,
invalid input HTTP 400, real Worker Summary and Display, an editable Attribute,
Work Queue permission discovery through Temporal visibility, missing Action
permission HTTP 403, successful Action, Channel consumption, Step completion and
engine closure. Log: `/private/tmp/dex-cleanup-http-e2e-final.log`.

| Path | FlowID | RunID | FlowType | Result |
| --- | --- | --- | --- | --- |
| Browser Start/Work Queue/Action | `dex-cleanup-ui-20261003` | `2a671c97-ec93-46f3-aeb6-ef37bea68a5a` | `webv2approval.Flow` | Completed; approval-status `approved` |
| HTTP regression | `dex-cleanup-api-9b0b39cb-6d58-477e-b697-34eb49076aa4` | `a976ca62-8e03-4371-9883-03b6e6e6be93` | `webv2approval.Flow` | Completed; approval-status `approved` |

For each execution, read-only CLI diagnostics ran before cleanup:

```bash
dexcli flow summary FLOW_ID --server 127.0.0.1:21801 --timeout 10s --no-hydrate
dexcli flow state FLOW_ID --server 127.0.0.1:21801 --timeout 10s --no-hydrate
dexcli flow history FLOW_ID --server 127.0.0.1:21801 --timeout 10s --page-size 25 --no-hydrate
```

The reads all succeeded. Histories contained Start, the durable Channel wait,
external publication, both Execute completions and FlowClosed. State showed
approval-status `approved`, the permission projection and the completed Step.
Each Step handler succeeded on its first attempt. Browser checks observed the
root header, Run canvas, local Start inputs and health check, Working as selector,
Work Queue item, Approve action, Completed result, and Debug execution graph/history.
Screenshot: `/private/tmp/dex-cleanup-validation/completed-run.jpg`.

A separate current `dexcli dev` process used local ports `21811/21812`, isolated
Connector/blob/log directories, and the isolated Temporal address `127.0.0.1:37233`.
It read generated FDGs from unchanged Connector sample and newsletter sources.
The browser loaded official GitHub v0.8.0 and Slack v0.11.0 metadata and rendered
local connection identities, release versions, local store paths, authorization
guides and the root OAuth callback URI. Slack's official Studio bundle was
checksum-verified and extracted. Its cache directory used archive digest
`7b5aea62282932f7c94b99ef28e133b94c090c9ac0ce184a3afdad1f2fab17e0`;
its `index.html` SHA-256 was
`4e69332e14f9a4a067c0b42c41467a04fcc309a451cb61ff00d49458da26f1f0`.
No provider credential was entered or grant changed. The final local dev Web
search reached the actual isolated Temporal backend and returned the completed
final-image execution above. Result: `/private/tmp/dex-cleanup-validation/local-dev-query.json`.

The final local dev command was:

```bash
dexcli dev --open=false --dex-port 21811 --web-port 21812 \
  --flow-rendering-dir /private/tmp/dex-cleanup-validation/definitions \
  --connector-config-dir /private/tmp/dex-cleanup-validation/local-connectors \
  --blob-store-dir /private/tmp/dex-cleanup-validation/local-blobs \
  --server-log-folder /private/tmp/dex-cleanup-validation/local-logs \
  --external-temporal-address 127.0.0.1:37233 --external-temporal-namespace default
```

The owned dev processes and port-forwards were stopped after diagnostics. Then
`kind delete cluster --name dex-cleanup --kubeconfig /private/tmp/dex-cleanup-kubeconfig`
removed only the temporary test node. `kind get clusters` still returned
`superverse-v2` and `sv-np-136`; the five temporary local ports were no longer
listening. Log: `/private/tmp/dex-cleanup-kind-cleanup.log`.

## Corrections and limits

Initial Go compilation found stale test-only removed fields and a missing YAML
import; those were corrected and the affected gates passed. Ordinary sandboxed
Go cache access was denied; authorized host checks used the existing cache.
Kind's default multi-platform import hit absent cached layers; explicit arm64
imports succeeded. The initial validation Deployment placed the global config
flag after `start`; its flag order was corrected and both real services converged.
The isolated Temporal initially listened only on its Pod IP, so its local forward
failed. Setting `BIND_ON_IP=0.0.0.0` in that test Deployment fixed the listener.
An existing Superverse IPv4 forward occupied port 27233; it was left untouched,
and the final cleanup forward used the distinct port 37233. The final local dev
backend query succeeded against this isolated service. An initial final CLI build
command targeted the module root, which has no Go files; rebuilding the actual
`./cmd/dexcli` package succeeded.

Two temporary HTTP validation runs waited on incorrectly named/formatted summary
status fields even though their Actions completed. Their exact executions were
reconciled using successful CLI summary/state/history reads, without replaying
effects: `dex-cleanup-api-b68e440b-2e98-4bdc-aeee-e57eb05a6516`, RunID
`d57c20d3-5fc7-43a8-895f-2dcb76bd9b0c`, and
`dex-cleanup-api-0af53c5a-cb72-4bc5-ba60-ce75ff2111dd`, RunID
`69df4c19-e511-4783-ae06-525cb635cf9f`, both `webv2approval.Flow`, Completed.
The corrected final run passed using the observed numeric completion code.
These were validation-harness failures, not reported as passing checks.

Real OAuth consent, token exchange/refresh, and credential-dependent Connector
Studio/provider operations remain unverified because no provider credentials or
consent were supplied. Existing PKCE, single-use state, multiple authentication
methods, token request declarations, RFC 6749 response handling, Slack mapping,
and refresh/ID-token scope proof tests passed, but contain scripted providers.
This cleanup does not establish application-provider or AWS acceptance.
Long retention and Continue-As-New were outside the changed Web scope and were
not revalidated as application business cases.

## Follow-ups

The application-visible removal requires dex-skills updates, but its baseline and
all affected language/Web references must wait for a published Dex release that
contains this commit. No skill baseline was changed from unreleased source.
When Superverse next upgrades its Dex pin, remove its forwarded-trust environment
entry and matching validator check. Neither Superverse file was changed here.
