# Production operations

Before reporting a mutation as effective or complete, identify its [write/read consistency and completion boundary](read-after-write.md). CLI `updated: true` and `skipped: true` acknowledge request acceptance. A subsequent Flow state read may block until the Flow processes the accepted request; search and projections can still lag.

Use this guide for Dex Server deployment, production inspection, and authorized recovery. Read [testing.md](testing.md) for verification scenarios and [troubleshooting.md](troubleshooting.md) for diagnostic routing.

## Diagnostic order

1. Capture the exact Flow ID, run ID, Flow type, SDK version, and Dex Server address. On server-zd, also capture the [run attempt](#server-zd).
2. Check Worker application logs for handler and connectivity errors.
3. Inspect the Flow in Dex Web.
4. Run a bounded read-only inspection:

```bash
dexcli flow summary <flow-id> --timeout 30s --no-hydrate
dexcli flow state <flow-id> --timeout 30s --no-hydrate
dexcli flow history <flow-id> --page-size 80 --timeout 30s --no-hydrate
```

5. Compare the active Step, Attributes, Channel waits, Timers, and recent semantic events with the intended graph.
6. Reproduce with the smallest matching integration test.
7. Fix application code or configuration, then decide whether existing executions need recovery.

## Dex Web pull routing diagnostics

With Server and CLI v1.15.0, Flow detail shows the Worker target and, for pull Flows, the Flow type queue and sticky app Worker ID. Active Steps and Step history show serving app Worker IDs for WaitFor and Execute, the dispatch sticky app Worker ID, and the queue source. **Sticky queue** means the serving ID matches the sticky ID captured for that dispatch; otherwise a completed dispatch shows **Flow type queue**. Compare against that captured ID, not just the Flow's current sticky ID.

Dispatch failures distinguish no available Worker, an unknown Step type or RPC name after the dispatch wait, and a Worker disconnect. Existing RPC history shows the serving app Worker ID for recorded invocations. Pending or failed dispatches without a serving Worker show **—** for Worker and queue source; older runs without metadata also show **—**. Address targets retain address-only routing. Step history still groups retries into its final method attempt and latest failure; routing does not provide a separate record for every retry or expand RPC history coverage. [Released routing UI](https://github.com/superdurable/dex/blob/server/v1.24.0/web/app/flows/details/PullRouting.tsx)

Correlate reserve-decline counters with total handler occupancy and **M − R** Step admission before increasing capacity; follow [reserved RPC capacity](pull-workers.md#reserved-rpc-capacity).

## Inspect pending Channel messages

Invoke the application's typed snapshot RPC to obtain a Channel's current FIFO values and server-assigned IDs. An ID disappears once its message is consumed or deleted. If delete or transactional move returns Channel-message-not-found, treat the local view as stale, invoke the snapshot RPC again, and let the user choose again.

Only pending Channel state is mutable through these operations. Editing a message means deleting it successfully and publishing a replacement with a new ID; it does not rewrite Flow history or application conversation Attributes.

Use a transactional RPC when deletion must commit atomically with a replacement publication or other Flow-state writes. Without transactional execution, a missing deletion may be a no-op while other effects commit; reconcile from a fresh list.

For an application queue UI, prefer one application snapshot RPC that returns durable conversation state, description, and loaded pending queues together. Refresh that snapshot after mutations and live events, on focus or reconnect, and periodically at low frequency. Keep optimistic items only as a short bridge; the snapshot is canonical.

Use **dexcli flow search**, **summary**, **state**, and **history** for narrower JSON output. Use **--no-hydrate** when payload contents are unnecessary or sensitive.

## Inspect raw Temporal payloads

With Dex CLI v1.4.2 or later, run the local protobuf Codec Server to inspect Dex's
internal Temporal payloads in Temporal Cloud or a local Temporal Web UI:

```bash
dexcli codec-server
```

Configure **http://127.0.0.1:8804** as the Codec Server URL in Temporal Web UI.
The browser connects directly to this process. Temporal Cloud and HTTP/HTTPS
origins on localhost or loopback IP addresses are accepted on any port, so an
automatically selected local UI port needs no extra server arguments. External
and malformed origins are rejected. The codec server binds only to loopback;
keep it local and stop it with Ctrl+C when finished. Leave access-token and
cross-origin credential forwarding disabled.

The codec converts known Dex binary protobuf payloads to ProtoJSON. Unknown
message types and other encodings pass through unchanged. The same tooling
applies to all five SDK languages. See the released
[CLI codec server usage](https://github.com/superdurable/dex/blob/cli-v1.24.0/cli/README.md#view-temporal-protobuf-payloads).

## Indexed Attribute capacity

The default local **dexcli dev** stack starts Temporal with SQLite. SQLite allocates a fixed number of custom Search Attribute slots per namespace and type. In a fresh local database, Dex uses three of those slots for system indexes, leaving this initial capacity for application Indexed Attributes:

| Dex index type | SQLite slots | Dex system indexes | Application slots initially left |
| --- | ---: | --- | ---: |
| **KEYWORD** | 10 | **FlowType**, **DexParentFlowID** | 8 |
| **KEYWORD_ARRAY** | 3 | **ActiveStepTypes** | 2 |
| **FULL_TEXT** | 3 | None | 3 |
| **BOOL**, **DATETIME**, **DOUBLE**, **INT** | 3 each | None | 3 each |

Index keys are shared across Flow types. Reusing one key with the same type does not consume another slot. Previously registered application keys remain in a persisted local database and reduce the current remainder. Worker startup fails while synchronizing indexes if a new key exceeds the remaining capacity.

Plan one namespace-wide pool before implementation. All application index keys must be explicit generic typed slots, reusable by every Flow type: **keyword1**, **keyword2**, **keywordList1**, **keywordList2**, **text1**, **text2**, **int1**, **int2**, **bool1**, **double1**, **datetime1**, and later numbers as needed within the available capacity. **keywordListN** uses **KEYWORD_ARRAY** and **textN** uses **FULL_TEXT**. Keep one type per physical key throughout the namespace; allocate different slots for different indexed fields within one Flow. Keep business meaning in Attribute names and document each Flow type's slot mapping. Application run searches must constrain **FlowType**. Parenthesize additional caller filters before combining them with that predicate. Raw SDK searches require this application predicate explicitly. Dex system indexes keep their reserved names. Do not index sensitive data or PII.

Changing a persisted local store's index key, type, or enabled state does not remove the old Search Attribute. Use a fresh local store and new ports while validating the new schema, or migrate the store deliberately.

Treat this as a local development constraint, not an application schema limit. Do not remove production query fields merely to fit local SQLite. To test more keys locally, point **dexcli dev** at an external Temporal deployment with enough visibility capacity using **--external-temporal-address**.

Production capacity is determined by its Temporal visibility backend. Elasticsearch-backed deployments do not have SQLite's fixed per-type slot pool, although Elasticsearch mapping limits can still apply. Temporal Cloud and self-hosted SQL visibility stores have their own limits. Verify the target environment against [Temporal's current Search Attribute limits](https://docs.temporal.io/search-attribute#custom-search-attribute-limits).

### xtemporal ParadeDB Dex visibility

With **xtemporal v1.32.1-x.5**, **paradedb-dex** and schema
**paradedb/visibility 2.0**, Search Attributes use one cluster-wide type map,
shared by every namespace. The operator pre-registers a generic pool before
Dex and application Workers start; creating a namespace does not register a
separate copy. Reserve **FlowType** and **DexParentFlowID** as Keyword and
**ActiveStepTypes** as KeywordList. Use **keyword1**–**keyword8**,
**keywordList1**–**keywordList2**, and **text1**–**text3**,
**int1**–**int3**, **bool1**–**bool3**, **double1**–**double3** and
**datetime1**–**datetime3** for application indexes so one schema fits the local
SQLite pool and this store. Dex **KEYWORD_ARRAY** maps to KeywordList and
**FULL_TEXT** maps to Text.

Keep **interpreter.attributeIndexesManagedExternally** at its default **false**
when the Operator Service is available. Normal Worker startup discovers existing
names, verifies declared types and creates only missing indexes. With a complete
pre-registered pool it performs discovery and type checks without adding names.
Real-service verification with Dex Server 1.13.0 and Go SDK 1.13.0 confirmed
Worker startup in a newly registered namespace, a Flow writing **int1** and
**keyword4**, its typed snapshot and indexed search; the cluster type map
was unchanged before and after. This mode differs from the Temporal Cloud
managed-external mode below, which deliberately skips discovery and verification.

Capacity checked **2026-10-21** against the pinned xtemporal release: the cluster
registration path imposes **no explicit per-type or total registration count
limit**. Its [operator add path](https://github.com/superdurable/xtemporal/blob/v1.32.1-x.5/service/frontend/operator_handler.go)
uses the [cluster metadata map](https://github.com/superdurable/xtemporal/blob/v1.32.1-x.5/common/searchattribute/manager.go),
and the [custom store](https://github.com/superdurable/xtemporal/blob/v1.32.1-x.5/x/paradedb/visibility_store.go)
adds no per-name DDL. The [schema](https://github.com/superdurable/xtemporal/blob/v1.32.1-x.5/schema/paradedb/visibility/versioned/v2.0/schema.sql)
uses typed JSONB buckets rather than SQLite's fixed slots. This is not an
unlimited-capacity guarantee: shared metadata, database and RPC payload sizes
still bound growth, and the pinned implementation specifies no maximum cluster
registration count. Separately, the [frontend defaults](https://github.com/superdurable/xtemporal/blob/v1.32.1-x.5/common/dynamicconfig/constants.go)
limit one execution's attributes to **100 keys**, **2 KiB per value** and
**40 KiB total**; these configurable write limits are not cluster slot quotas.
Keep the pool fixed rather than allocating new names for each tenant or Flow.

The store rejects **STARTS_WITH** and ordering by custom attributes. The Dex
profile can additionally disable all explicit ordering and the Count API with
**system.visibilityDisableOrderByClause** and
**xtemporal.frontend.disableCountWorkflowExecutions**; when enabled, do not
issue Count calls or rely on custom sort order. Use bounded, namespace-scoped
search pages and the returned cursor. See the pinned
[query support and profile settings](https://github.com/superdurable/xtemporal/blob/v1.32.1-x.5/docs/xtemporal/paradedb-dex-store.md).

## FDG 2.0 management metadata

Dex Web can inspect Runs created by every SDK, but the current FDG 2.0
management-interface metadata is generated only by the Go analyzer. This
includes Indexed Attribute presentation, Summary and Display RPC fields, Action
forms and permissions, groups and explanations, and Action input capture hints.
Do not claim that Java, Python, TypeScript, or Rust source can publish this
metadata today. Those SDKs can operate Flows and use the supported Client and
CLI surfaces, while a Dex Web v2 management experience that depends on this
metadata currently requires a Go Flow definition.

## Local Connector credentials

Dex Web v2 can configure exact published official Connector modules, a release or a release candidate, for Go Flows during loopback **dexcli dev**. The Flow must use operation-specific factories, a static connection name, and static names for any Trigger bindings. Generic factories, local replacements, unpublished module versions, or version conflicts remain visible but are not configurable.

The default plaintext development store is `~/.dex/connectors/connections.json`. Pass **--connector-config-dir** to choose an isolated directory. Use the absolute path displayed in Connections mode when starting an application that reads the file:

```bash
DEX_CONNECTOR_CONFIG_FILE="$HOME/.dex/connectors/connections.json" <your-app-command>
```

The file survives Dex Web and application restarts. Restarting Dex Web clears pending OAuth/PKCE exchanges, submitted OAuth client secrets, and Studio UI sessions because those remain in memory, unless the Connector release maps a client ID or secret to a stored credential. Dex Web stores the access token after checking the granted scopes, does not refresh it, and stores a refresh token only when the release maps one. Stored credentials remain until expiry or explicit local deletion. Deletion does not revoke the provider grant.

Dex Web only writes this file, and the Connector SDK release line decides whether an application can read it. Connector releases before v0.21.0 generate `NewLocalConnection`, and the SDK's `localconfig.LoadFromEnvironment` reads the file; `sdkgo/localconfig` exists through sdkgo v0.21.0 and is absent from sdkgo v0.22.0. Every connector from v0.21.0 generates only `NewProjectConnection`, which takes the project configuration that `projectconfig.LoadFromEnvironment` loads (see [Deployed Connector configuration](#deployed-connector-configuration)). From `sdkgo` v0.28.0, `LoadFromEnvironment` reads `connections.json` and `use-configurations.json` itself when **DEX_CONNECTOR_CONFIG_FILE** names the file, so the launch command above works for a connector at v0.21.0 or later with no `DEX_PROJECT_*` variables and no object storage; setting both is an error. Settings, Trigger bindings, and operation settings are read at startup and need an application restart to change, while reauthorization in Dex Web does not. A refreshed credential is written back to the file and Trigger inboxes persist beside it, so run one application per directory. Through `sdkgo` v0.27.0 nothing reads these files for a connector at v0.21.0 or later: report that as a compatibility blocker and have the application require `sdkgo` v0.28.0 or later; never write a reader for the file. The library resolved the former gap in [dex-connectors-library#230](https://github.com/superdurable/dex-connectors-library/issues/230). Only the Go Connector SDK provides either loader. Never put the JSON record, access token, API key, or secret-bearing generated value into Flow state, logs, browser messages, or application responses.

Connection credentials and Trigger binding matchers are separate. One connection may serve several Flows without sharing their filters. Trigger delivery is at least once: use deterministic Flow IDs for starts. Application RPCs own their redelivery policy, bounded deduplication state when needed, and business locks.

A release for an authorization server that the connection owner chooses, such as a company identity provider, declares its authorization endpoint, token endpoint, scopes, or identity endpoint as connection settings. Fill those settings before choosing **Authorize**: Dex Web rejects a missing or blank setting, an empty scope list, a URL that is not absolute, or an unsupported client authentication method before it contacts the provider. An endpoint written in a release must use HTTPS. An endpoint typed into a setting may use **http** only when every address its host resolves to is loopback or private, and Dex Web refuses link-local, unspecified, and multicast addresses even over HTTPS. It uses no proxy and follows no redirect for these requests. Over a permitted **http** address the authorization code, client secret, and tokens still cross the network unencrypted.

## Deployed Connector configuration

Dex Web's Connector setup APIs run only in loopback-bound **dexcli dev** with local Flow Definition Graph files. A deployed Dex Server, its Dex Web, and any non-loopback listener do not expose them, and Dex (Server and Web) provides no deployed Connector configuration, OAuth, or credential store. The plaintext `connections.json` is a development store: never copy it into an image or a deployed volume.

A deployed Go application reads its connections with the Connector SDK's `sdkgo/projectconfig` ([README](https://github.com/superdurable/dex-connectors-library/blob/sdkgo/v0.25.0/sdkgo/projectconfig/README.md), [loader](https://github.com/superdurable/dex-connectors-library/blob/sdkgo/v0.25.0/sdkgo/projectconfig/environment.go), both at `sdkgo/v0.25.0`):

- At startup it calls `projectconfig.LoadFromEnvironment(ctx)`. The loader reads the `DEX_PROJECT_*` variables (project ID and scope, the exact configuration key, version and digest, the storage bucket and prefix, and the KMS key ARN) and the AWS default credential chain, then loads the pinned, secret-free configuration snapshot from S3-compatible object storage.
- A hosting platform or operator writes that configuration document and the connection records. The application never writes them.
- Each connector's generated `NewProjectConnection(project, "connection-name")` decodes the connection's settings once and resolves its credentials from the same storage during every call, so a replaced credential needs no restart and a settings change does. Set the same static `ConnectionName` on every operation-specific factory config.
- From sdkgo v0.25.0 an entry may carry `sharedConnectionId`. The application then reads the credential from the credential broker that the configuration document declares, keeps it in memory only, and never calls the connector's refresh driver.
- Shared connections (sdkgo v0.26.0 or later; [README](https://github.com/superdurable/dex-connectors-library/blob/sdkgo/v0.27.0/sdkgo/projectconfig/README.md#shared-connections), read at `sdkgo/v0.27.0`). A host platform may let users configure a connection once and let several applications select it; the application only declares connection names and the platform decides which connection each name uses. The broker never sends host-kept credential fields, which are refresh tokens, client secrets and service account keys, so an application never sees them. A connector decodes a shared credential without them through its generated `UnmarshalSharedCredential` (a connector version without it fails on a shared entry). A connection name selects one connection with one set of settings: different settings need a different connection name, and Step operation configuration stays in the application.
- A connection selection or a settings edit reaches an application only through a new configuration document, at the next Preview start or Live deployment. A broker answer `CONNECTION_NOT_SELECTED` (`ErrConnectionNotSelected`) is always a terminal error; the application never follows a changed selection.
- Refresh of a stored credential is admitted once: only the winning invocation may dispatch the provider exchange, a retry joins the admitted attempt, and expiry never grants a second dispatch.

Decide with the user who writes the configuration, where the storage lives, and how the application role reaches it, before handoff. Keep secrets out of source, Flow state, rendered definitions, and logs, and prove with the real application startup that every Connector connection resolves its settings and credentials. Only the Go Connector SDK provides this loader. For another SDK language no equivalent SDK exists today, so do not invent one.

## Deploy Dex Server components

The `dex-server` image starts Web, API, Interpreter, and Matching in one OS process by default. It serves FlowService gRPC on port 8801 and Dex Web HTTP on port 8802.

### Public API authentication

Public FlowService authentication is off by default. Configure `auth.authenticators` to require a bearer credential on public calls and streams. Static token authenticators read SHA-256 hashes from a YAML token file; JWT authenticators verify issuer, audience, signature, and validity against a cached key set. Health checks remain open. Protect the plaintext gRPC network path, and keep token values out of source, logs, Flow state, and rendered definitions. [Released authentication configuration](https://github.com/superdurable/dex/blob/server/v1.24.0/server/config/config_template.yaml)

Clients and Workers have separate credential options in all five SDKs. Supply a rotating file credential where available, or a current-token provider that can be called for every FlowService call and each pull stream opening. With auth enabled, a missing credential is rejected and a failed credential prevents the call; a rotated token is picked up on a later call or stream reconnect. See the language handbook for exact names.

When auth is enabled and Dex Web runs, set `web.flowServiceCredentialFile` to a bearer-token file. This authenticates Web's calls to FlowService; it does not authenticate Web users. Anyone who reaches the Web port can act with Web's credential, so protect that port with network controls or an authenticating proxy. This file is distinct from the local Connector development credential store.

Auth requires `internal.listen` and `internal.tokenFile`. The separate listener serves private InternalService and MatchingService calls; Interpreter Workers use the first internal token, and the listener accepts all valid lines for rotation. Use a token of at least 32 characters per line, restrict the listener to Dex replicas, and protect its plaintext network path. A separate Interpreter deployment must target the private listener. With auth off, existing public InternalService targets remain supported unless a private listener is configured.

#### FlowService TLS

Go SDK v1.22.0 adds **ClientOptions.FlowServiceTLS** and **WorkerOptions.FlowServiceTLS**, each a pointer to **dex.FlowServiceTLSOptions**. Set each independently for a TLS-terminating FlowService endpoint, including a pull Worker's task streams. Nil keeps plaintext; a non-nil empty option verifies with system roots. TLS requires version 1.2 or newer, negotiates ALPN **h2**, verifies the server certificate, and never falls back to plaintext. A gateway must accept HTTP/2 and forward gRPC over HTTP/2. The option affects only that Client's or Worker's outbound FlowService connection. It does not encrypt a listening Worker's inbound WorkerService listener.

With TLS, **FlowServiceAddress** accepts **host:port**, **dns:///host:port**, or **dns://authority/host:port**, with a port from 1 through 65535. The address host is the default certificate name and SNI; the default HTTP/2 **:authority** includes the host and port. DNS resolver authority does not become the certificate name or HTTP/2 authority. **FlowServiceTLSOptions.AdditionalRootCAsPEM** adds PEM root certificates to the system roots; an empty value uses system roots alone. **FlowServiceTLSOptions.ServerName** overrides the certificate name, SNI, and HTTP/2 authority when the certificate differs from the address host. Supply a DNS name or IP without a port. A nonempty override is sent as **:authority** without a port. An IP address sends no SNI and needs a matching IP SAN. Leave **ServerName** empty for an IPv6 endpoint or use a DNS name: an IPv6 override produces an unbracketed **:authority** that strict HTTP/2 servers reject. Go's option has no client-certificate or private-key field, so it does not configure mutual TLS.

**NewClient** and **NewWorker** reject an invalid TLS address or **ServerName**, a port outside 1–65535, a failure to load system roots when adding CAs, or a nonempty PEM bundle without a parseable certificate. Connections open on demand. Certificate, name, and handshake failures appear as gRPC **Unavailable** on the first Client call or from **Worker.Start**. An unavailable **GetServerInfo** reports a FlowService connection failure; an unimplemented call retains the incompatible-server diagnosis. Bearer, file, and API key Credentials work over either transport. **APIKeyCredential** exchanges with its token endpoint through its own HTTP client and trust settings; **FlowServiceTLS** does not configure that connection.

At Server v1.20.0, the Go Server's **api.port** and server-zd v0.1.6's **api.listen** serve plaintext FlowService gRPC. Terminate TLS in a gateway or load balancer in front of either Server. **dexcli** has separate **--tls**, **--tls-ca-file**, and **--tls-server-name** settings for its own FlowService connection. Dex Web's **web.flowServiceTarget** uses plaintext gRPC; its bearer-token file controls authentication, not transport encryption. [Go TLS API](https://github.com/superdurable/dex/blob/sdk-go/v1.24.0/sdk-go/dex/flow_service_tls.go) · [Go Server config](https://github.com/superdurable/dex/blob/server/v1.24.0/server/config/config.go) · [server-zd v0.1.6 release](https://github.com/superdurable/dex/releases/tag/server-zd/v0.1.6) · [CLI TLS](#dexcli-tls-transport)

At the v1.20.0 language baselines, Java **ClientOptions** and **WorkerOptions**, Python **ClientOptions** and **WorkerOptions**, TypeScript **ClientOptions** and **WorkerOptions**, and Rust **ClientOptions** and **WorkerOptions** have no FlowService TLS option; their default FlowService connections use plaintext. Do not apply the Go option names to those SDKs. See each language handbook for its pinned source.

#### API key Credential

SDK v1.16.0 in all five languages adds an API key Credential. It exchanges a long-lived API key for short-lived access tokens and sends each access token as the bearer credential. Use it when a token endpoint issues access tokens that the Server's JWT authenticator accepts, and the application holds an API key. Use the file credential instead when another process writes and rotates the access token in a file.

The application supplies three values: the token URL, the server ID, and the API key. The server ID names the Dex Server that the endpoint scopes the token to. The SDK never derives it from a host name. Each exchange POSTs the JSON body **{"server_id": ..., "api_key": ...}** to the token URL. A successful response is HTTP 200 with a non-empty **access_token**, a **token_type** of Bearer, and a positive **expires_in** in seconds.

- **Caching and refresh:** the credential caches the token. After 80% of its lifetime, one refresh runs while calls keep using the valid token. Concurrent callers share a single in-flight exchange. A failed refresh keeps the valid token and retries at most every five seconds. Each exchange times out after 10 seconds by default. Expiry counts from the start of the exchange request, so a token that expired before its response arrived is never used.
- **Rejected tokens:** when Dex rejects a call with UNAUTHENTICATED, the SDK discards that token, but only while it is still the cached one. A unary call is retried exactly once with a fresh token. The retry is safe because Dex rejects an unauthenticated call before executing it. A rejected pull Worker poll stream reconnects once immediately with a fresh token. A second rejection follows the ordinary [permanent rejection path](pull-workers.md#startup-registration-and-readiness) and its retry delay. File and custom credentials get neither the unary retry nor the immediate reconnect.
- **Transport security:** the token URL must use HTTPS. Plain HTTP is accepted only for localhost, 127.0.0.0/8, and ::1. The credential never follows a redirect, so the API key reaches only the configured URL. The Dex connection defaults to plaintext gRPC; Go SDK v1.22.0 can use [FlowService TLS](#flowservice-tls). Protect every plaintext network path separately.
- **Redaction:** the credential's formatted value and its exchange errors never contain the API key or an access token. Errors redact the key, recently issued tokens, and every JWT-shaped substring: dot-separated base64url segments of at least 10 characters each. This rule can also hide other text of that shape, such as a long dotted host name. Endpoint **error** and **error_description** fields are truncated to 200 characters.
- **Ownership:** the application owns the credential and may share one instance among Clients and Workers. Closing a Client or Worker never closes or stops it. Lifecycle differs by language: Rust exchanges during construction and fails calls with an expired token until a refresh succeeds, while Java requires closing the credential after its last Client and Worker. Follow the language handbook for construction, options, errors, and close semantics.

Keep the API key in deployment secret management. Never put it in source, Flow state, logs, or rendered definitions. Dex Web still authenticates to FlowService only through **web.flowServiceCredentialFile**.

#### dexcli credentials

Configure `dexcli` with either a bearer token or an API key, not both. For a bearer token, `--credential-file` takes precedence over `DEX_CREDENTIAL_FILE`, which takes precedence over `DEX_CREDENTIAL`. The CLI has no raw-token flag. Store a token in a protected file when possible; avoid putting one into shell history or process arguments. [Released CLI options](https://github.com/superdurable/dex/blob/cli-v1.24.0/cli/internal/command/options.go)

With CLI v1.16.0, an API key uses three settings:

- `--api-key-file` reads the key from a file and takes precedence over `DEX_API_KEY`. There is no flag for the key value itself, and surrounding whitespace is ignored.
- `--token-url` or `DEX_TOKEN_URL` names the token endpoint, with the same HTTPS and loopback rule as the SDKs.
- `--server-id` or `DEX_SERVER_ID` names the server that the endpoint scopes the token to. `dexcli` never derives it from `--server`.

Combining API key settings with any bearer token setting is a usage error that exits with status 2. So is setting `--token-url` or `--server-id` without an API key. Each command exchanges the key for a new token before its first call. A failed exchange fails the command before any FlowService call. A long command refreshes the token in the background after 80% of its lifetime. Unlike the SDKs, `dexcli` does not retry a call that Dex rejects with UNAUTHENTICATED; rerun the command to obtain a fresh token. Errors redact the key and tokens as the SDK credentials do. [Released CLI authentication guide](https://github.com/superdurable/dex/blob/cli-v1.24.0/cli/README.md)

```bash
export DEX_API_KEY="$(cat ~/.dex/api-key)"
export DEX_TOKEN_URL=https://auth.example.com/oauth/token
export DEX_SERVER_ID=orders-production
dexcli flow search --query 'ExecutionStatus = "Running"'

dexcli health --api-key-file ~/.dex/api-key \
  --token-url https://auth.example.com/oauth/token --server-id orders-production
```

#### dexcli TLS transport

CLI v1.18.0 or later can reach a Dex endpoint behind a TLS-terminating gateway or load balancer. Commands still use plaintext gRPC by default. The TLS settings apply to every command that connects to FlowService, including `dexcli health` and `dexcli version check`.

- `--tls` or `DEX_TLS=true` dials with TLS 1.2 or later and verifies the server certificate against the system root CAs. `DEX_TLS` accepts boolean values such as `true`, `false`, `1`, and `0`; any other value is a usage error. `--tls=false` overrides `DEX_TLS=true`.
- The certificate must match the `--server` host, which `dexcli` also sends as the TLS server name (SNI). Set `--tls-server-name` or `DEX_TLS_SERVER_NAME` when the certificate names another host, for example when `--server` is an IP address.
- `--tls-ca-file` or `DEX_TLS_CA_FILE` adds the PEM certificates in the file to the system root CAs, for a server certificate issued by a private or self-signed CA. A file without a PEM certificate fails the command.
- `dexcli` negotiates HTTP/2 with ALPN `h2`. A TLS-terminating load balancer or gateway must accept `h2` and forward gRPC to Dex over HTTP/2.

Setting `--tls-ca-file` or `--tls-server-name` without TLS is a usage error that exits with status 2. TLS never falls back to plaintext. The bearer token or API key access token is sent on every call over either transport. Without TLS it crosses the network unencrypted, so use plaintext only on loopback or a protected network. These settings configure only the `dexcli` FlowService connection. The API key exchange at `--token-url` always verifies the token endpoint against the system root CAs. [Released CLI TLS guide](https://github.com/superdurable/dex/blob/cli-v1.24.0/cli/README.md#connect-over-tls)

```bash
dexcli --server orders.dex.example.com:443 --tls \
  --api-key-file ~/.dex/api-key \
  --token-url https://auth.example.com/oauth/token --server-id orders-production \
  flow search --query 'ExecutionStatus = "Running"'
```

### Worker readiness and capacity diagnosis

With Go SDK v1.13.0 or Java, Python, TypeScript, and Rust SDK v1.14.0, gate startup and replacement deployment on the language's pull registration wait, then combine its readiness snapshot with process lifecycle health. Do not drain old Workers until replacements register. Inspect each queue's last permanent error and registered-stream state: a rejected surplus stream leaves readiness intact while another stream is registered. Stop alone preserves readiness, so remove stopping Workers from service separately. Consider opt-in rejection shutdown only with supervised restarts and cancellation-responsive handlers. Continue handling blocking run failures after readiness; TypeScript reports fatal rejection through **run()**, and Rust exposes the typed rejection through **pull_registration_error()**. Follow the [registration contract and language API names](pull-workers.md#startup-registration-and-readiness).

With Server v1.13.0, Client API **ResourceExhausted** can identify capacity or rate limits rather than an unknown application failure. Inspect status details and available retry hints, check configured capacity and load, and follow the [bounded retry and reconciliation guidance](error-handling.md#capacity-and-rate-limit-failures). Do not confuse this with a rejected pull registration.

### Pull Worker server setup

The `matching` role owns pull Worker queues. Keep at least one Matching instance running. API instances accept outbound Worker streams on FlowService and forward them to the queue owner. Preserve bidirectional HTTP/2 streaming and make load-balancer idle timeouts longer than `matching.pingInterval` (20 seconds by default). A single Server process uses local membership. For multiple instances, configure `membership` with reachable seeds and a private `internal.listen` plus `internal.tokenFile` on every process. All instances must share the intended backend namespace and membership cluster; route public traffic only to API instances. [Released routing configuration](https://github.com/superdurable/dex/blob/server/v1.24.0/server/config/config_template.yaml)

From SDK v1.23.0, SDKs send an upstream Pong every 20 seconds while a task runs. Set proxy and load balancer idle timeouts above 20 seconds and **matching.pingInterval**; older SDKs need a timeout above the longest task.

A matching-only process (`dex-server start --services matching`) requires both `internal.listen` and `internal.tokenFile`, even when public auth is off. Its MatchingService is never public. [Released bootstrap check](https://github.com/superdurable/dex/blob/server/v1.24.0/server/service/bootstrap/bootstrap.go)

`matching.dispatchWait` defaults to 10 seconds, including up to five seconds of `matching.stickyWait`. `matching.localDispatchWait` defaults to 3 seconds; `localStickyWait` and `localExecutionReserve` preserve time for local Step execution. A Worker that rejects an unregistered Step type or RPC name is excluded from that dispatch, so another capable pull Worker can accept within the applicable wait. Capacity defaults are `maxConcurrentTasksCap: 256` streams per app Worker ID per matching owner, `maxStreams: 100000` per Matching instance, and `maxQueueLength`, `maxFlowTypes`, and `maxFlowTypeLength` of 256 each. With SDK v1.14.1 in all five languages, surplus idle streams close gracefully after load drops without clearing readiness. The steady-state stream bound per Worker is **M + (F + 1) × I**, where **M** is maximum concurrent tasks, **F** is registered Flow types, and **I** is idle streams per queue. Burst peaks remain bounded by **matching.maxConcurrentTasksCap** and **matching.maxStreams**. Check the [pull Worker stream bound](pull-workers.md#heartbeats-capacity-and-shutdown) before raising Worker concurrency or Flow type count, and budget rolling overlap within both caps.

### Worker wake hook

Configure `worker.wake.url` and optional `worker.wake.budget` (120 seconds by default) when an unavailable Worker can be started on demand. Dex posts a best-effort `kind: wake` notification with the target, and a rate-limited `kind: active` notification after a successful dispatch. For pull routing, the wake target is the Flow type queue; Dex sends it after the sticky wait and keeps the same dispatch attempt open. For listening routing, the budget holds connection retries without consuming new attempts. An internal token is sent when `internal.tokenFile` is configured. Size the dispatch wait and Step retry policy to cover actual startup time. [Released wake settings](https://github.com/superdurable/dex/blob/server/v1.24.0/server/config/auth.go)

### Removed multi-tenant configuration

Multi-tenant mode has been removed since Server v1.10.0. Single-tenant Flow data is preserved, but a configuration containing the removed `tenancy` section fails startup. Move `tenancy.authenticators` to `auth.authenticators`, `tenancy.internal` to `internal`, and `tenancy.wake` to `worker.wake`; drop `subjectPattern` and `claims`, and use only SHA-256 entries in static token files. Also remove `blobStore.tenantCleanupConfirmationDelay` and each `attributeStore.stores.<name>.tenantColumn`: both are rejected at startup. If a destination table still requires a tenant column, migrate that table separately so single-tenant writes satisfy its schema. Inspect the deployed YAML and destination schema before upgrading. [Released migration error](https://github.com/superdurable/dex/blob/server/v1.24.0/server/config/removed_tenancy_config.go)

Before deploying a Worker, run `dexcli version check` against its target Server. Each Server and SDK release declares an inclusive protocol interval. Worker startup selects the highest common protocol before synchronizing Attribute indexes and binding WorkerService. Artifact versions are diagnostic only.

When upgrading from a Server that predates `GetServerInfo`, upgrade the Server before any SDK. New Workers reject a missing information RPC. After that first upgrade, either side may be newer while the intervals overlap.

A breaking Server release raises its minimum protocol. Stop old Workers and their traffic before upgrading the Server, upgrade every SDK in the maintenance window, then restart traffic. Isolated blue-green environments may run disjoint intervals, but they must not communicate. Running Workers do not renegotiate after a Server upgrade.

Use `dex-server start --services <selection>` to scale components independently. The selection must be a nonempty comma-separated combination of `web`, `api`, `interpreter`, and `matching`:

```bash
dex-server start --services web
dex-server start --services api
dex-server start --services interpreter
dex-server start --services matching
dex-server start --services web,api
```

Web-only does not initialize storage, Temporal, Cadence, the index synchronizer, the Interpreter, or Matching. Configure its remote API target in YAML:

```yaml
web:
  bindAddress: 0.0.0.0
  port: 8802
  flowServiceTarget: dex-api:8801
  flowRenderingDirectory: ""
```

When `web.flowServiceTarget` is empty, Web connects to `localhost:<api.port>`. The connection uses plaintext gRPC and `api.grpcMaxMessageBytes`. Put TLS, authentication, and network policy at the deployment boundary.

`/healthz` reports Web process liveness, not upstream API readiness. Web starts while FlowService is unavailable; `/api/*` returns the existing mapped gRPC error until the API recovers.

`/readyz` validates the current Flow definition snapshot and then checks FlowService. With Server and CLI v1.17.0, Web asks FlowService for its engine. Against server-zd, readiness calls the FlowService health check, because a search can exceed the builtin search scan budget there. Against the Go Server, readiness still runs a one-result Flow search, because the Go Server's health check does not reach Temporal or Cadence. Use `/readyz` for traffic gating and `/healthz` for liveness. [Released Web readiness](https://github.com/superdurable/dex/blob/server/v1.24.0/web/README.md#run-through-the-dex-server-image)

For Interpreter-only deployments without `internal.listen`, point `interpreter.interpreterActivityConfig.internalServiceTarget` at the API service. With a private listener, the Interpreter uses its local address by default. Scale Web, API, Interpreter, and Matching replicas according to HTTP traffic, FlowService traffic, execution load, and pull task load respectively.

```yaml
interpreter:
  interpreterActivityConfig:
    internalServiceTarget: dex-api:8801
```

API and Interpreter replicas must use the same intended Temporal namespace or Cadence domain and compatible production storage configuration.

### Temporal Cloud attribute indexes

Temporal Cloud API-key clients cannot call the Operator Service methods that
discover and create Search Attributes. Provision the following Dex system
indexes through the Temporal Cloud control plane before starting Dex:

- `FlowType`: Keyword
- `DexParentFlowID`: Keyword
- `ActiveStepTypes`: KeywordList

Provision every indexed application Attribute before its Worker starts. Then
configure every Dex Server process that uses the namespace:

```yaml
interpreter:
  attributeIndexesManagedExternally: true
```

This mode validates application declarations but deliberately does not discover,
create, or verify indexes. A missing or incorrectly typed index therefore fails
when Temporal first uses it; treat Cloud provisioning and Worker startup as one
deployment contract.

When multiple Server processes use Streams, configure a shared Redis 7+ backend. The in-memory backend is only for one process:

```yaml
streamStore:
  backend: redis
  redisURL: redis://redis:6379/0
```

Configure Redis with `noeviction` so capacity pressure becomes a visible Stream write failure. If Blob Store is enabled, use the same durable object-store configuration across API and Interpreter replicas; do not rely on pod-local blob directories.

[Hard-delete Attributes](data-handling.md#hard-delete-attributes-for-short-lived-secrets) need Server v1.11.0 or later with Blob Store enabled. Blob Store is enabled by default, and `blobStore.enabled: false` makes the Server reject hard-delete writes. Dex deletes a hard-delete value from the Blob Store on delete or a null write. Backups and bucket versioning outside Dex can still keep copies. Disable or restrict them for the blob bucket when deleted secrets must not survive. Deleted values are not restored by time travel or by a Flow retry.

Blob Store keeps payloads through 100 bytes inline by default and offloads from 101 bytes. Keep the storage ID short because it appears in every durable reference; prefer a name such as `p1` over `production1`. A reference such as `p1|c/ab3de7kp2x` uses the lowercase Base36 day offset from September 1, 2026 UTC and a deterministic lowercase Base36 object ID. It omits the Flow ID and encoding. The Server derives a physical path such as `260913$coding-session--c08256c4/ab3de7kp2x` from trusted context. ASCII letters, digits, hyphens, and underscores remain readable in the Flow ID segment; other UTF-8 bytes use uppercase percent escaping. Run IDs and Step execution IDs in input-snapshot paths remain Base64URL encoded. Object Blobs store the complete EncodedObject. `objectIdLength` defaults to 10; zero selects that default, negative values are invalid, and any positive length is accepted. Every Server sharing a namespace must use the same immutable value. Use 12 or 16 for unusually high per-Flow daily object counts. Values above 50 only add leading zero padding because the ID derives from SHA-256. Readers accept any nonempty lowercase Base36 object ID. Application code must treat references as opaque.

Successful ASYNC local Step input snapshots are disabled by default. Enable `blobStore.asyncStepInputSnapshotsEnabled` only when semantic history must retain the exact inputs sent to those methods. The setting is independent of the payload offload threshold and does not affect Flow execution, retry, or recovery. When disabled, no snapshot objects are written and the corresponding semantic-history inputs are unavailable. SYNC and regular-fallback inputs remain available from Flow history.

## Attribute Store destinations

Create every destination table or collection before starting the Server. Dex never creates, migrates, or backfills a destination, and one invalid destination fails Server startup. Startup validates each SQL table's primary key and columns, and every schema refresh (one minute by default) validates them again. A new nullable or defaulted column needs no Dex restart, as long as its migration completes before the Workers that write it start. From Dex Server v1.6.1, a batch that writes a column missing from the current schema, in the store's table or an AttributeMap table, refreshes that store's schema before it writes; a column that a refresh already found missing waits for the periodic refresh. A Server before v1.6.1 discovers every new column only at the periodic refresh. Removing or changing a column makes later writes be filtered or retried.

The `rowKey`, `columnNaming`, and `attributeMaps` store settings need Dex Server v1.6.0 or later, and an older Server rejects them at startup. Upgrade every Server before adding them to a shared configuration. With `rowKey` the key columns come from the Flow ID, so a Flow ID that does not match its template skips the batch with an error log. Watch the Server log for filtered writes, skipped batches, and exhausted retries, because a projection failure does not roll back Flow state. Design guidance is in [row keys, column names, and AttributeMap tables](data-handling.md#row-keys-column-names-and-attributemap-tables), and the full contract is the [Custom Attribute Store](https://docs.superdurable.io/production/attribute-store) page.

## server-zd

server-zd is a Dex Server whose only runtime dependency is object storage. It serves the same FlowService API, so the SDKs work unchanged, but it dispatches only to [pull Workers](pull-workers.md). Configure every Worker with the language's pull options and start Flows with the pull Worker target. It is released separately from Dex Server. Follow the [server-zd operation guide](https://docs.superdurable.io/production/server-zd-operations) for deployment, the lease, object storage, metrics, and the configuration mapping from the Go Server.

Step failure policies, including Execute failure routes, WaitFor proceed-on-failure, and timeout-handler failure routes, run with Go Server semantics from server-zd v0.1.2. Earlier releases reject them at StartFlow and fail the attempt of a Step decision that sets one. See [recovery error types](error-handling.md#recovery-error-types).

When a Step is canceled by another Step's decision, an RPC decision, StopFlow, Flow closing, or time travel, server-zd sends the cancellation to the pull Worker. The handler observes it through its SDK's cancellation signal even if it records no heartbeat. On the Go Server, a regular Step attempt observes cancellation when the handler records a heartbeat or writes a Stream frame; the ASYNC local phase is canceled directly. A regular handler that sends neither can keep running until it returns or its method timeout cancels it. A heartbeat timeout fails the attempt and can start a retry while the old handler still runs. Both servers discard a canceled Step's result. For cancellation to reach long-running handlers on every Dex Server, record heartbeats within **HeartbeatTimeout**, stop promptly when the SDK reports cancellation, and keep side effects idempotent.

server-zd does not serve Dex Web. Run a web-only `dex-server start --services web` and set `web.flowServiceTarget` to the server-zd plaintext gRPC address. That process creates no Temporal or Cadence connection. When server-zd authentication is on, also set `web.flowServiceCredentialFile`.

From server-zd v0.1.6, `search.paradeDbConnectionString` accepts PostgreSQL URL or keyword-string TLS parameters: `sslmode=disable|allow|prefer|require|verify-ca|verify-full`, `sslrootcert` as a PEM CA file or `system` roots, `sslsni=0|1`, and `sslnegotiation=postgres|direct`. Direct negotiation requires PostgreSQL 17 or later; `sslmode=allow` with `sslnegotiation=direct` is invalid. Explicit `sslmode=prefer` first attempts TLS without certificate verification and uses plaintext when the server declines TLS. A string without TLS parameters stays plaintext.

Always set `sslmode` explicitly. Without it, any other TLS parameter (an `sslrootcert` file, `sslsni`, or `sslnegotiation=postgres`) selects `prefer`: TLS without certificate verification, with plaintext fallback, even when `sslrootcert` names a CA file. `sslrootcert=system` instead forces verify-full. server-zd still reads that file at startup but never uses it to verify. With `sslnegotiation=direct`, a missing or `prefer` mode becomes `require`, which never falls back to plaintext. `sslmode=allow` behaves like `prefer` and tries TLS first, unlike libpq and pgx, which try plaintext first. `sslmode=verify-ca` verifies the certificate chain but not the host name, using system roots when `sslrootcert` is absent. For a verified connection, use `sslmode=verify-full` with `sslrootcert`, or `sslrootcert=system`.

`sslmode=require` without `sslrootcert` encrypts without verifying the server certificate; with a PEM `sslrootcert` file it verifies the certificate chain but not the host name. `sslmode=verify-full` verifies the chain and host name against the supplied roots, or system roots when `sslrootcert` is absent. `sslrootcert=system` forces verify-full. `connect_timeout` is in seconds and bounds the TLS handshake and authentication, including reconnects; without it, the connection timeout is ten seconds. Connection errors include TLS and I/O causes but never the connection string. See the [server-zd v0.1.6 release](https://github.com/superdurable/dex/releases/tag/server-zd/v0.1.6) and [operation guide](https://docs.superdurable.io/production/server-zd-operations#configuration-mapping).

Attribute Store synchronization is available from server-zd v0.1.3. Its `attributeStore` section uses the Go Server's keys and [destination table contract](#attribute-store-destinations), with these differences:

- Only `type: postgres` is supported. Any other store type fails startup, so keep MySQL, Databricks, Snowflake, and MongoDB stores on the Go Server.
- Synchronization is asynchronous. Step, RPC, and SetAttributes commits do not wait for the table write, so treat the table as eventually consistent. Rows normally appear within about a second of the commit.
- While a store is unavailable, Flows keep running and the backlog is written later with the latest values. Entries still pending `syncRetryPolicy.totalDuration` after they were first marked are dropped; the Go Server measures it per batch instead. Later writes of the same Attributes sync normally.
- After time travel, server-zd rewrites every row the run synced with the state after the time travel.
- From server-zd v0.1.4, a natural close caused by a completing or failing Step decision, exhausted Step retries, or the Flow timeout waits for every pending Attribute Sync entry to be written or dropped, as on the Go Server. The run remains RUNNING, and WaitForFlow keeps waiting. v0.1.3 closed without waiting, so its rows could land after Flow completion. When entries are dropped, Flow completion does not prove the table is current.
- On server-zd v0.1.4 or later, a natural close also waits for transactional and locking RPCs admitted before cleanup, even without an Attribute Store. Those RPCs can finish and add synced writes to the wait. During cleanup, SetAttributes, PublishToChannel, and non-transactional RPCs are accepted; their synced writes can extend the wait, but they start no Step. A transactional RPC is rejected even when it only reads. New transactional or locking RPCs, WaitForStepCompletion, WaitForAttributeMatch, WaitForAttributeMapInstanceMatch, and Server DeleteChannelMessage fail with FAILED_PRECONDITION and, from server-zd v0.1.5, sub-status FLOW_CLOSING. The error detail remains "flow terminal cleanup is in progress".
- On the Go Server with Temporal, Flow close cleanup waits for admitted transactional and locking RPCs after a natural close or while a stopped run winds down. During cleanup, new transactional or locking RPCs, WaitForStepCompletion, WaitForAttributeMatch, WaitForAttributeMapInstanceMatch, and Server DeleteChannelMessage fail with FAILED_PRECONDITION; from Server v1.20.0, the sub-status is FLOW_CLOSING. With Cadence, locking RPCs and these waits are unsupported, and transactional RPCs and DeleteChannelMessage do not use this cleanup rejection. With `api.useTemporalSynchronousUpdateForAllRPCs: true`, every RPC is rejected during cleanup, including non-transactional reads.
- On server-zd v0.1.4 or later, `syncRetryPolicy.totalDuration` bounds each pending entry's retry window, not the whole close. Entries for stores that are no longer configured are dropped with an error log, so the close can finish.
- On server-zd, StopFlow and Terminate close the run at once, even during cleanup. Remaining entries keep syncing after the close. Through v0.1.4, a later Server SetAttributes on that run returns NOT_FOUND with "Flow run is not found for flow ID ...". From v0.1.5, Server SetAttributes can erase currently stored hard-delete Attributes on a closed, unpurged run when every write sets one to null and the request sets no action permission mappings. The run stays closed and its stored objects are deleted; value writes, ordinary erases, and mixed requests still return NOT_FOUND with FLOW_NOT_EXISTS. The Go Server can accept writes while a canceled or failed run winds down, but rejects writes after the run closes. Write what the Flow needs before stopping it.

- From server-zd v0.1.7, node start retries transient or unknown-outcome object-storage reads with jittered backoff. **recovery.startupReadRetryBudget** (default 30 seconds) bounds each read separately, and permanent errors fail start immediately. A reset to a closed point closes the restored run through the close barrier, and PostgreSQL connection errors redact their causes.
- From server-zd v0.1.8, Attribute Sync never replaces a pending synced value with a later unsynced write, and a reset that restores an unsynced Attribute over a synced one deletes its store row.
- Each database needs a Dex fencing table, named by `fencingTableName` (default `dexzd_attribute_sync_fencing`). Create it as the table owner and grant `SELECT`, `INSERT`, and `UPDATE` on it to the Dex role. `dex-server-zd` never creates or alters tables, and startup fails when the fencing table is missing.

The [server-zd Attribute Store guide](https://docs.superdurable.io/production/server-zd-operations#attribute-store) has the fencing table statement, retry behavior, and sync metrics. See the [server-zd v0.1.5 release](https://github.com/superdurable/dex/releases/tag/server-zd/v0.1.5) for the closed-run erase and FLOW_CLOSING, the [v0.1.4 release](https://github.com/superdurable/dex/releases/tag/server-zd/v0.1.4) for the close barrier, and the [v0.1.3 release](https://github.com/superdurable/dex/releases/tag/server-zd/v0.1.3) for the first Attribute Store support.

For local development, CLI v1.16.0 or later runs `dexcli dev --engine zd`, which starts `dex-server-zd` and Dex Web without Temporal. The default engine remains Temporal.

- Install `dex-server-zd` from a server-zd release on PATH, set `DEX_SERVER_ZD_BINARY`, or pass `--zd-binary`.
- State persists in `$HOME/.dex/zd/<dex-port>` unless `--zd-data-dir` names another directory.
- With server-zd v0.1.4 or later, dev defaults to process-crash-safe durability: acknowledged writes survive a process kill, but not necessarily a host crash or power loss. It takes over its previous lease at once when the data directory supports file locks. An uncleanly stopped relaxed data directory refuses startup; the error names the directory and says to delete it. A directory that an earlier release stopped gracefully works in every mode. A directory left after an earlier release crashed or was killed is refused in the default and `relaxed` modes. Delete that directory to start empty, or set `dev.durabilityMode: strict` in the `--zd-config` file for `dexcli dev --engine zd` only if every earlier run used strict. After the first start with this rule, the directory is stamped and any mode works.
- `--zd-config FILE` passes a `dex-server-zd` YAML file with configuration overrides, such as closed Flow retention, Streams, or the search backend.
- Logs go to standard error; `--server-log-folder` also writes `dex-server-zd.log`.
- From CLI v1.19.0, `--attribute-store-config FILE` works with `--engine zd`. The CLI does not check the binary version, so use `dex-server-zd` v0.1.3 or later. The CLI passes the file to `dex-server-zd`, which reads only its `attributeStore` section. The file must define at least one store, and the `--zd-config` file must not also set `attributeStore`. The default engine and the Go Server reject `fencingTableName`. If they also load this file, omit that key; `dex-server-zd` then uses its default `dexzd_attribute_sync_fencing` table name. The database still needs the destination tables and the fencing table. Through CLI v1.18.x, `--attribute-store-config` cannot be combined with `--engine zd`.
- Temporal and Go Server options, such as `--sqlite-db-filename`, `--external-temporal-address`, `--verbose-engine-log`, and `--blob-store-dir`, cannot be combined with `--engine zd`.

[Released CLI engine options](https://github.com/superdurable/dex/blob/cli-v1.24.0/cli/README.md)

On server-zd, time travel keeps the run ID and creates a new run attempt. With Server and CLI v1.17.0, Dex Web shows the run attempt and switches between attempts, and `GET /api/flows/summary` reports `runAttempt`. `dexcli flow summary` does not print it, so read it in Dex Web. On the Go Server, time travel creates a new run ID and `runAttempt` is zero. [Released Web behavior](https://github.com/superdurable/dex/blob/server/v1.24.0/web/README.md#run-against-server-zd)

## Safe recovery

Diagnosis is read-only by default. Stop, time travel, publish, invoke, skip a Timer, or mutate Attributes only when the user asks to change the Flow.

Before a mutation:

- resolve the current exact run, including its run attempt on [server-zd](#server-zd)
- explain the expected state change
- use an application Flow RPC or an explicit dexcli operation
- satisfy any explicit confirmation flag
- re-inspect the Flow afterward

Time travel is appropriate after deploying a code fix when replaying from a safe Step boundary will not duplicate an unprotected side effect. Preserve the original business resource, Flow ID, and operation identity; capture the exact diagnostic run and Step method before mutation. Reconcile provider objects and previously accepted writes first. Do not replace the account or resource merely to avoid its failed history. If a safe boundary cannot be established, design an explicit recovery or compensation Step instead.

A time-travel reset cannot invent a missing method boundary or rewrite the original Start options. A failure before the first Execute may have no Execute reset point. Do not silently replace it with a fresh Flow. If a corrected start is needed, obtain authorization for that recovery, prove the old execution is closed, reconcile effects, and preserve the intended Flow ID and logical request identity under the exact reuse policy. Closed-execution reuse is not cross-run request deduplication; application admission must protect external effects. Where a recorded WaitFor boundary is appropriate, select that method explicitly and decide whether replaying later writes is safe before choosing `--skip-writes-reapply`.

When a durable-history bug has already failed a Flow, validate the fix against that
same history when safe:

1. Deploy or restart the Worker with the corrected code.
2. Time travel the failed Flow to the last safe execution before the failure.
3. Let Dex replay and continue with the corrected Worker.
4. Re-inspect the new run and verify the formerly failing path and durable state. On server-zd, the new run keeps its run ID and has the next run attempt.

This is stronger than testing only a new Flow because it exercises recovery from the
actual recorded history. Do not cross an external side effect unless it is idempotent,
compensated, or explicitly safe to repeat.

Sources:

- Dex CLI: https://docs.superdurable.io/references/cli
- Application operations: https://docs.superdurable.io/production/application-operations
- Versioning: https://docs.superdurable.io/production/application-operations#versioning-flow-code
