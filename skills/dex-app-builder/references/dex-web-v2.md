# Dex Web v2 and FDG 2.0

Reference capability baselines: Dex Web `web/v1.30.0`, `cli-v1.30.0`, the
server-zd release it bundles, and the SDK source baseline in
[bundle-baselines.md](../../dex-sdk/references/core/bundle-baselines.md).
These validate the guidance, not an instruction to upgrade an application.
An application retains its pinned Server, CLI, Web, and Go SDK versions unless the
user authorizes an upgrade. Dex Web v2 provides Start Flow on pull Workers and
the permission-based Work Queue. Release-owned Connector setup runs only in
loopback `dexcli dev`.

## Run Dex Web

`dexcli dev` starts server-zd and an embedded Dex Web. A deployment runs the standalone `dex-web` binary or the `superdurable/dex-web` image next to the Dex Server. The Dex Server does not serve Dex Web, and no `dex-server` process or image does either. `dex-web` connects to an existing FlowService and never starts a Server. [Released standalone Web](https://github.com/superdurable/dex/blob/web/v1.30.0/web/README.md#run-standalone-dex-web)

```bash
dex-web --flow-service-address dex-server-zd:8801 \
  --flow-service-credential-file /etc/dex/web-token
```

Every setting is a flag or an environment variable, and a flag overrides its variable. Dex Web has no `web.*` YAML keys.

| Flag | Environment variable | Default |
| --- | --- | --- |
| `--bind-address` | `DEX_WEB_BIND_ADDRESS` | `127.0.0.1` |
| `--port` | `DEX_WEB_HTTP_PORT_NUMBER` | `8802` |
| `--flow-service-address` | `DEX_WEB_FLOW_SERVICE_ADDRESS` | `127.0.0.1:8801` |
| `--flow-rendering-dir` | `DEX_WEB_FLOW_RENDERING_DIRECTORY` | empty |
| `--tls` | `DEX_WEB_TLS` | `false` |
| `--tls-ca-file` | `DEX_WEB_TLS_CA_FILE` | empty; system roots |
| `--tls-server-name` | `DEX_WEB_TLS_SERVER_NAME` | empty; the target host |
| `--flow-service-credential-file` | `DEX_WEB_FLOW_SERVICE_CREDENTIAL_FILE` | empty; no credential |

`--start-flow-worker-target-pull` and `--start-flow-worker-target-headless`, with their `DEX_WEB_START_FLOW_WORKER_TARGET_*` variables, are deprecated no-ops. Dex Web accepts them and prints a startup warning. Remove them from scripts and manifests.

The `superdurable/dex-web` image binds `0.0.0.0:8802` and runs as UID and GID 65532. Mount definition directories and token files read-only with permissions that UID can read. The `main` branch publishes `superdurable/dex-web:latest`, and a `web/vX.Y.Z` release publishes `superdurable/dex-web:vX.Y.Z`. Pin a release tag for a deployment.

With Dex Server auth enabled, set `--flow-service-credential-file` for Web's calls to FlowService. This does not authenticate Web users: protect the Web port with network controls or an authenticating proxy. Keep this bearer-token file separate from the local Connector development credential store. Use `--tls` when Web sends the credential to a remote FlowService. See [public API authentication](../../dex-sdk/references/core/operations.md#public-api-authentication).

Released connector modules may require an older Connector SDK; at connectors
`main` `980a6f9`, modules require exact releases from `sdkgo/v0.7.0` through
`sdkgo/v0.10.0`. Go minimum version selection builds the application with its
own newer Dex Go SDK requirement, so keep the application pin and do not wait
for connector re-releases.

Web v2 is Go-only. Validate every Flow with the v2 analyzer and never fall back to v1.

## Run and Work Queue surfaces

Dex Web derives its experience from:

- indexed Attributes for list and search columns;
- `GetDexSummary` for additional read-only list fields;
- `GetDexDisplay` for run detail fields;
- Step groups and explanations for the run timeline and the Flow canvas, which
  folds a group into a stage only when it is one run or one choice and draws
  failure handling only on request;
- waits on Channels/ChannelMaps for actionable human work;
- eligible Action RPCs for operator operations.

## Pull routing and debugging

Flow detail shows the Flow type queue and sticky app Worker ID. Active Steps and Step history show serving app Worker IDs for WaitFor and Execute, the dispatch sticky app Worker ID, and the queue source. **Sticky queue** means the serving ID matches the sticky ID captured for that dispatch; otherwise a completed dispatch shows **Flow type queue**. Compare against that captured ID, not just the Flow's current sticky ID.

Dispatch failures distinguish no available Worker, an unknown Step type or RPC name after the dispatch wait, and a Worker disconnect. Existing RPC history shows the serving app Worker ID for recorded invocations. Pending or failed dispatches without a serving Worker show **—** for Worker and queue source; older runs without metadata also show **—**. Step history still groups retries into its final method attempt and latest failure; routing does not provide a separate record for every retry or expand RPC history coverage.

Use the captured dispatch sticky ID, queue source, and failure reason before changing Worker configuration. Inspect [reserve pressure and capacity](../../dex-sdk/references/core/pull-workers.md#reserved-rpc-capacity) when registered Workers decline Steps.

## Dex Web with server-zd

Dex Web reads `GetServerInfo` and reports the engine in `GET /api/v2/catalog` as `serverEngine`: `zd`, or `unspecified` when the FlowService does not answer. Locally, `dexcli dev` serves server-zd and Dex Web together. [Released Web behavior](https://github.com/superdurable/dex/blob/web/v1.30.0/web/README.md#run-against-server-zd)

- **Start Flow** always starts on pull Workers. The dialog asks only for the Flow ID and the Start input and has no Worker address or headless control.
- Time travel keeps the run ID and creates a new run attempt. The run details page, which v1 run pages and v2 **Inspect** share, replaces **Run chain** with a **Run attempt** selector. Choosing an earlier attempt adds `?runAttempt=N` to the URL and shows that attempt's history. Live history and live state always belong to the current attempt. A **TimeTravelFork** event links to the previous attempt.
- `GET /api/flows/history` and `POST /api/flows/time-travel` accept an optional `runAttempt`; zero or absent selects the current attempt. `GET /api/flows/summary` and each history event report `runAttempt`.
- `/readyz` calls the FlowService health check instead of a Flow search.

Record the run attempt with the run ID in verification evidence.

## Management UI design

Treat Dex Web v2 as the first management interface, not merely a Flow
inspector. Model one business record as one Run. Use Indexed Attributes for
supported filters, the Summary RPC for additional list fields, the Display RPC
for detail fields and UI slots, and RPC Action metadata for buttons, typed input
forms, current-state conditions, and permissions. Use editable scalar Display
Attributes for simple updates. Work Queue discovers Runs from cumulative Action
permission history, then the opened Run rechecks current Action eligibility.
Timeline, Execution graph, execution details, and recovery information cover
process progress and failures. From web/v1.29.0 the Run page loads only the
newest 1,000 history events; Timeline pages back to older events, but
Execution graph, Overview, and the v2 canvas show only that window. Expose
long-running business progress through Display fields, not Execution graph.

Design those contracts before proposing a custom management backend or UI. A
custom surface requires a recorded interaction Dex Web v2 cannot provide; the
fact that a product needs administration does not establish such a gap.

**Working as** selects one declared Action permission and filters Work Queue candidates. The request carries it as `workQueuePermissions`. Dex Web neither authenticates a user nor enforces authorization, and the selection grants nothing. It refuses an Action whose required permission is missing from the request's `workQueuePermissions` with 403, but it reads that list from the request itself, so the check is a consistency check, not authorization.

A deployed Dex Web sits behind a trusted application boundary that authenticates users, maps roles to permissions, and limits which permissions it sends. Do not expose Dex Web or the FlowService API directly to the public Internet.

## Start Flow

The v2 Run workspace shows **Start Flow** when the selected definition has a
supported typed Start schema. The operator enters a Flow ID and the typed
input. Dex Web starts every Flow on pull Workers and sends no Worker address.
The browser cannot supply Step options. A Worker built with SDK v1.25.0 or
later is a pull Worker and needs no routing setting.

Dex Web validates input and derives `skipWaitFor` from the validated Start
phase: `execute` skips WaitFor, while `wait_for+execute` retains it. Missing,
repeated, or unknown phases are invalid definitions. Do not rewrite an
execute-only application Step into a dummy WaitFor to work around admission.
Check the actual graph, source, installed Server, and Worker registration.

Dex Web generates a new request ID for every start and sets no ID reuse
policy, so the Server's default, which disallows reusing an existing Flow ID,
rejects a second submission of the same Flow ID. That rejection is not a
recovery path. After an uncertain start response, read the Flow ID with the
bounded read-only `dexcli flow summary` before submitting again, and treat a
different Flow ID as a different business operation. Definition revision,
and typed input errors must fail with their actual coded errors.
Connector Trigger applications still use the real Trigger for their Trigger
acceptance path.

**POST /api/v2/start** accepts **flowType**, **flowId**, raw JSON **input**, and an
optional **workerTargetPull: true**. It returns HTTP 400 for
**workerTargetAddress** or **workerTargetPull: false**, because Dex routes all
Flow work to pull Workers. It accepts and ignores **bypassWorkerHealthCheck**.
Dex Web does not check that a pull Worker is currently registered before
starting because registration can change before dispatch.
**POST /api/v2/worker-health** still reports whether Dex Web can open a TCP
connection to a **host:port**; it does not invoke a Worker method.

Observed with Dex CLI v0.13.8 and Go SDK v0.12.1:

- Dex Web loads local definitions only from
  `dexcli dev --flow-rendering-dir DIRECTORY`, which holds the FDG 2.0 JSON
  files. Without it the catalog is empty. Regenerate the JSON after every Flow
  change.
- Start Flow sends the FDG's Flow and Step type names, which must match the
  Worker's registrations. Follow the SDK's [default type-name rule](../../dex-sdk/references/go/versioning.md#default-flow-and-step-type-names).
  Keep derived names for new definitions. Go SDK v1.5.0 and later register
  names without the package, and `dexcli` v1.5.0 and later generate matching
  names. Pair a Go SDK before v1.5.0 with a `dexcli` release before v1.5.0.
  If the graph and Worker names differ,
  diagnose the analyzer/SDK version or generated metadata; do not implement
  `GetFlowType` and `GetStepType` on every Flow and Step as a Web workaround.
- Older releases invoked `WaitFor` on an execute-only Start Step and
  failed before Execute. Dex Web derives the correct option from
  the validated graph; preserve the real Step semantics and inspect any existing
  failed execution before an authorized recovery.
- FDG 2.0 requires `GetDexSummary` and `GetDexDisplay`, both registered as
  RPCs, even when a Flow has little to show (`v2_view_rpc`).
- A start input field of type `map[string]any` cannot drive the form
  (`v2_start_input`).

For a scripted check, use the same endpoints as the browser:
`GET /api/v2/catalog` returns `definitionRevision`;
`GET /api/v2/connector-connections` reports each connection's status;
and `POST /api/v2/start` accepts `flowType`, `flowId`, an optional
`workerTargetPull: true`, and raw JSON `input` with that revision in the
`X-Dex-Flow-Definition-Revision` header.

## Connectors mode

In loopback **dexcli dev**, the **Connectors** tab at `/v2/connectors` groups Connector Steps and Trigger bindings by connector and static connection name, showing each connector by its release display name. It shows the exact module version, dependent Flows, Steps, operations, and bindings, plus **Missing**, **Ready**, **Expired**, **Conflict**, or **Unsupported** status. The Step drawer links the same identity to its setup page. `/v2/connections` links redirect there.

A connector whose manifest declares `selection: multiple` holds several auth methods in one connection, such as several LLM providers, each added as its own card. Editing a saved connection keeps a stored secret when its field is left blank.

Automatic setup requires an operation-specific factory from an exact official published module, a release or an `-rc.N` release candidate, a static `ConnectionName`, and no local module replacement. Different module versions for one connector/name key are a blocking conflict. Generic factories and unsupported dependencies still render the Flow but cannot write credentials.

For connector development only, `dexcli dev` accepts
`--connector-release-override connector-id=artifact-directory`. Use an artifact
built from the same local connector source and display the visible **Local
override** status. Do not treat an override as a published release or commit it
as an application dependency. Never make an unreleased connector look released
by serving its working tree as the declared next version into the default Go
module cache; that copy persists in `GOMODCACHE` and shadows or conflicts with
the real release. Use the override, or a temporary `GOMODCACHE` for any
consumer that must resolve pre-release versions.

Dex Web verifies release metadata and the Studio artifact. A supported Studio bundle runs in an opaque-origin sandbox; otherwise the host renders the manifest form. Neither surface receives stored credential values. PKCE state and UI sessions are memory-only, and so are the OAuth client ID and secret unless the release maps them to stored credentials.

A release for an authorization server that the connection owner chooses, such as a company identity provider, writes its authorization endpoint, token endpoint, scopes, or identity endpoint as connection settings instead of constants. Fill those settings in before **Authorize**: Dex Web checks them first and rejects a missing or blank value, an empty scope list, a non-absolute URL, or an unsupported client authentication method, with an error that names the setting. A setting may supply an `http` endpoint only when every resolved address is loopback or private; an endpoint a release writes must use HTTPS. Dex Web does not refresh tokens, so a Connector that renews its own credential reads the saved settings.

Connections presents authorization first, then nested operation and Trigger
configuration tabs. A Flow's static `ConnectorConfigurationUI` composes ordered
release-owned units. Each binding maps one generated unit port to one declared
RFC 6901 JSON Pointer; the host rejects undeclared paths. Dex Web writes only
non-secret operation configuration to sibling `use-configurations.json`, keyed
by connector, connection, operation, Flow type, and Step type. An application
whose connector release reads the Dex Web files loads them once at startup, so a
configuration edit requires an application restart; see
[connector architecture](connector-architecture.md#local-development-configuration)
for which releases do.

The connector release owns setup guidance for the complete rendered surface.
Dex Web shows the authorization guide's provider start URL and ordered steps,
then shows each manifest default below its field as parenthetical guidance.
Descriptions explain units, valid format, override use, and blank behavior.
Operation and Trigger unit descriptions explain the choice in context. Verified
OAuth or OpenID Connect claims and declared read-only provider pickers should
produce identity and resource fields instead of asking the user to retype them.
An application consuming a released connector must not add duplicate free-text
configuration for those derived values.

Studio Host API 0.2 keeps the iframe at an opaque origin and accepts only
nonce-bound protocol messages. `use.configuration.save` writes the scoped
non-secret object. `connector.frame.resize` reports bounded content height.
The generic `provider.command.execute` broker can execute only a command
declared by that exact connector release: HTTPS destination, credential field,
fixed/request parameters, redirect bound, response-size bound, and backend
capability are host-enforced. The host injects credentials and rejects any
response that reflects a secret. Tokens, app secrets, authorization headers,
connection files, and stored credential values never enter the iframe.

Connection credentials and Trigger matcher configuration are separate records. Changing one binding does not change another Flow that reuses the same connection. Slack Studio writes stable channel and member IDs while displaying names, raw IDs, copy controls, and manual-ID fallback. Provider tokens and app secrets remain host-owned and are never sent to the Studio iframe.

Use `--connector-config-dir DIRECTORY` to isolate a local stack. Dex Web shows
the resolved connection and operation-configuration paths. The plaintext
development credential file must never be committed or uploaded. Restarting
Dex Web preserves stored connection, binding, and use configuration but clears
pending OAuth/PKCE exchanges and UI sessions. Credential replacement is read
for each provider call; non-secret connection, binding, and operation
configuration remains startup-bound in the application.

A deployed standalone Dex Web does not expose Connector setup, OAuth, or
credential storage; they exist only in loopback `dexcli dev`. A deployed Go
application reads its connections through the Connector SDK's
`sdkgo/projectconfig`; see
[deployed Connector configuration](../../dex-sdk/references/core/operations.md#deployed-connector-configuration).
Test the actual native display and declared Action path with the real Worker,
and separately verify the authentication boundary in front of the deployed Dex
Web.

Configuration fields that can be obtained from verified claims, profile APIs,
or declared read-only setup commands render as derived read-only values rather
than duplicate text inputs. Every remaining normal or OAuth field keeps the
connector-owned start URL, exact provider page path, creation or lookup steps,
format, units, secret status, blank semantics, and
parenthesized manifest default.

**POST /api/v2/search** accepts several permissions; a run matches any requested permission, then Flow type and other filters apply with AND. A historical permission match discovers work that is or was available. Dex Web rechecks current Action eligibility when the run opens.

## Source layout

For each Flow, keep all of these in one Go file:

- Step and Flow definitions;
- Attributes and other persistence declarations;
- Action input structs;
- `GetDexSummary`, `GetDexDisplay`, and Action handlers;
- all `dex:*` directives;
- transitions and Dex decisions.

## Directives

Use the focused [Go FDG authoring reference](fdg-authoring.md)
for exact field, input, indexed Attribute and typed Action syntax.

### QR capture hint

A user-sourced string Action input may add `capture:qr-code` to its `dex:input`
directive. FDG 2.0 emits `capture: "qr-code"`. No other capture value is
supported, and Attribute-sourced or non-string fields fail analysis. Omitting
the hint preserves the ordinary text input.

Dex Web keeps that text input and adds **Scan QR code**. The scanner loads only
after a user opens it, prefers the rear camera, and copies the decoded text into
the field without submitting the Action. Pasting, a keyboard-style scanner, and
manual input remain available. Success, cancellation, errors, Run changes, and
unmounting release the camera tracks.

Camera access requires HTTPS or localhost. An embedding host must allow camera
access in its Permissions Policy. A denied permission, missing camera, or
insecure context leaves manual input available. Scanning does not authenticate
the operator, grant a permission, satisfy an Action condition, validate the
business value, or bypass the RPC. The embedding host remains responsible for
identity, role-to-permission mapping, and the camera policy.

## Permission projection

The Go Worker includes the complete Action permission mapping only when a successful WaitFor, Execute, timeout Execute, or RPC invocation writes or deletes an Action condition source. Unrelated writes, failed invocations, and query-only RPCs omit it. Initial Flow and SubFlow state receives its projection directly from the Go SDK, including RPC-only Flows.

The Server overlays those business writes on authoritative Attribute state, evaluates every mapping, sorts and deduplicates newly matching permissions, and atomically adds them to `DexWorkQueuePermissions`. Once matched, a permission remains in that Flow's Work Queue permission history and remains searchable after completion. Empty mappings and later state changes do not remove history. Application Steps do not need projection-only Attribute locks.

Dex Web follows the same rule for editable fields: only `SetAttributes` calls that modify an Action condition source include the complete mapping. An Action definition is a stable contract for an active Flow. A changed definition is evaluated on a later source write or a new Flow. Removing or renaming a permission does not clear existing history.

Permission history is discovery data, not authorization evidence or proof that an Action remains eligible. Every Action RPC rechecks current business state. The trusted application boundary authorizes who may invoke the RPC.

## Validation

Use the [Go FDG validation and diagnostic reference](fdg-authoring.md#focused-verification).
Validate every Flow source file in the application. Keep locally rendered
definitions in the existing `--flow-rendering-dir`, and package the same
validated definitions for a deployed Dex Web as described in
[verification and handoff](verification-handoff.md#package-flow-definitions).
