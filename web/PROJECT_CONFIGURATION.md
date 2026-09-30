# Project configuration protocol

Project Dex Server/Web owns one fixed Live or Preview configuration scope. It
interprets exact, checksum-verified connector releases. There is no platform
credential broker, connector-specific Go import, or separate configuration
service. The application uses the same shared storage protocol through the
Connector SDK.

## Dex Flow Changes

None. No Flow, Step, Attribute, Channel, or Stream is introduced by this Web
component. The shared versioned object store is the single owner of configuration
and credential admission. An application Release pins an immutable ordinary
configuration version while resolving the latest credential head by logical
connection identity. Platform Flows may retain the accepted snapshot reference;
they must not mirror credential state or temporary OAuth secrets.

## Database Schema Changes

None. The storage prerequisite is a private, versioned S3 bucket. Hosted storage
requires an exact KMS key ARN. Explicit local fixtures may disable KMS. Dex Web
and the application use the same `sdkgo/projectconfig` implementation, with
create-only and ETag conditional writes. The core package imports no Dex SDK
runtime; its separate typed provider adapter may import the application SDK.
This separation avoids registering both server and SDK protobuf descriptors in
the same Dex binary.

## Startup and trust boundary

`web.projectConfiguration` fixes `storageId`, `prefix`, `projectId`, `scopeKind`,
`sessionId`, `kmsKeyId`, and `allowUnencrypted`. Scope kind is `live` or `preview`;
only Preview accepts and requires a Session ID. The internal bearer token comes
from `DEX_WEB_PROJECT_CONFIGURATION_ADMIN_TOKEN`. It is never returned to a
browser. Browser requests cannot select a bucket, prefix, project, or Session.

Port 8802 must be reachable only by the authenticated proxy or trusted internal
callers. The proxy strips browser-supplied forwarding and actor headers before
injecting:

- `X-Forwarded-Prefix`: `/dex/projects/<project>/configuration/live` or the
  corresponding stable Preview mount including its Session ID.
- `X-Forwarded-Proto`, `X-Forwarded-Host`: the public browser origin.
- `X-Dex-Web-Embedded: true`.
- `X-Dex-Web-CSRF-Token`: the authenticated browser's CSRF token.
- `X-Dex-Actor-ID`: the authenticated, stable account identity.

Browser mutations carry `X-Dex-CSRF-Token`, the public `Origin`, and explicit
`X-Dex-App-Manifest-Revision`, `X-Dex-Configuration-Revision`, and, when relevant,
`X-Dex-Credential-Revision`. The proxy also validates its own browser CSRF token.
OAuth callbacks are bound to the original actor, fixed scope, stable callback
URI, and ten-minute expiry. OAuth does not depend on a Release ID.

The proxy must deny browser access to the internal AppManifest PUT and validation
POST. A bearer token on these endpoints authenticates the platform server, not
an end user. The internal GET may use the same bearer token; the browser GET
requires trusted actor metadata.

## HTTP contract

Revisions are unsigned 64-bit JSON integers. An absent configuration document
has revision zero. All public digests use lowercase `sha256:<hex>`.

`PUT /api/v2/project-configuration/app-manifest` accepts:

```json
{
  "expectedRevision": 0,
  "sourceCommit": "0123456789012345678901234567890123456789",
  "manifestDigest": "sha256:<digest-of-exact-source-bytes>",
  "manifest": "<exact UTF-8 dex-app.yaml JSON source>"
}
```

The manifest string preserves source bytes; a JSON object would permit
reformatting and invalidate the source digest. The existing
`superverse.dev/dex-app/v1` schema declares application port/health path, Flow
source paths, and connections. Each connection contains `connectorId`,
`connectionName`, exact official `modulePath`, exact `version`, `authMethodId`,
`operations`, and optional `triggerBindings` with `triggerName` and `bindingName`.
There is no connector-ID-to-module-path fallback. A connector release with a
single legacy auth declaration uses the explicit method ID `default`.

Both PUT and `GET /api/v2/project-configuration` return safe metadata:

```json
{
  "appManifestRevision": 1,
  "sourceCommit": "0123456789012345678901234567890123456789",
  "manifestDigest": "sha256:<source-digest>",
  "connectorContractDigest": "sha256:<connector-contract-digest>",
  "environmentContractDigest": "sha256:<environment-contract-digest>"
}
```

GET additionally returns `configurationRevision` and the fixed `scope` object.
It contains no credential object reference, secret value, or arbitrary URL.
The canonical connector digest sorts connections by name, operations by value,
and trigger bindings by trigger name then binding name. Each connector map
always includes `triggerBindings`, using `[]` when empty. JSON map keys are
lexically ordered. Environment digest fields match the source extractor:
`port`, `healthPath`, `publicBaseUrlRequired`, and
`connectorConfigurationRequired`.

`POST /api/v2/project-configuration/validate` accepts:

```json
{
  "appManifestRevision": 1,
  "manifestDigest": "sha256:<source-digest>",
  "configurationRevision": 2
}
```

It validates every declared connection and trigger binding against the exact
verified connector release. An unsupported trigger schema is an explicit error.
The result adds `status: "READY"`, `configurationRevision`, and
`snapshot: {key, version, digest, mediaType}` to the safe metadata. Snapshot keys
are relative to the fixed startup prefix and are exactly:

```text
projects/<project>/live/configuration/head
projects/<project>/preview/<session>/configuration/head
```

The platform obtains bucket and outer prefix only from trusted configuration,
checks this relative key, and binds the returned immutable reference to its
Release. The SDK reads the exact version and verifies its digest. The snapshot
contains ordinary fields and logical connection identities, never credentials.
For a project without connections, initial validation creates the empty revision
one configuration document and returns that accepted revision.

## UI/UX

`/v2/connectors` uses AppManifest declarations in project mode. It can save
API-key credentials and complete OAuth before a build exists. The response
reports stored credential field names and safe status, never stored values.
Changing ordinary settings and changing credentials have separate CAS owners;
a failed credential replacement cannot overwrite another credential mutation.
Reload after a conflict rather than silently resubmitting stale values.

A declared trigger uses its connector release's
`spec.triggers[].configuration.fields`. A missing configuration schema is
unsupported; an explicit empty field list allows an empty settings object.
Generic fields support strings, HTTPS URLs, integer numbers, booleans,
durations, string lists/maps, and enums. String lists may declare allowed values
and uniqueness. Manifest-declared durations are normalized to integer
nanoseconds before storage for strict Go runtime decoders. The form renders
stored durations with an explicit `ns` unit.

Trigger binding names come from AppManifest, never a fabricated FDG. Operations
that use Flow/Step-specific configuration still require a real FDG. Runs and
Actions retain their original exact-definition and permission admission.
Configuration readiness does not grant Runs access.

## OAuth and credential recovery

OAuth records are scoped under:

```text
<outer-prefix>/projects/<project>/live/oauth/<sha256(state)>/{head,seed,result}
<outer-prefix>/projects/<project>/preview/<session>/oauth/<sha256(state)>/{head,seed,result}
```

`head` contains actor, scope, exact manifest/configuration/credential revisions,
expiry, attempt identity, status, and the exact private seed reference. `seed`
contains the PKCE verifier, client secret, and original declaration. `result`
contains a private provider response written before identity derivation and
credential publication. None is a browser or Flow payload.

The shared ConnectionStore conditionally admits an exchange before any token
POST. Only the invocation winning this admission may dispatch. Another replica
or restarted process may recover an immutable result but may not replay the
provider call. Unknown provider outcomes remain fenced. A new authorization
attempt can replace them with an explicit fresh revision. Later replacement or
revocation fences every earlier callback. After successful publication, recovery
checks the exact immutable credential envelope's attempt and fence; it cannot
mistake an unrelated later credential for the original result.

The SDK refreshes only on expired credential use. Dex Web does not run a refresh
scheduler and does not infer refresh eligibility from arbitrary HTTP 401s.

OAuth authorization expires logically after ten minutes. Infrastructure must
configure one-day expiration of current and noncurrent versions under each
exact OAuth prefix, including abandoned attempts. S3 lifecycle scheduling is
not immediate erasure at ten minutes. The shared ObjectStore does not yet expose
physical version deletion; project deletion must delete its entire owned scope.
Non-OAuth configuration and credential retention must remain independent.

## Tests

From this worktree's `web` directory, after building embedded assets:

```bash
npm ci --offline
npm run check
npm run build
GOWORK=/path/to/local-integration.go.work go test -race ./...
DEX_PROJECT_CONFIG_TEST_ENDPOINT=http://127.0.0.1:29000 \
DEX_PROJECT_CONFIG_TEST_BUCKET=<owned-versioned-local-bucket> \
GOWORK=/path/to/local-integration.go.work \
go test -race . -run '^TestProjectConfigurationRealS3' -v -count=1
```

AWS credentials are supplied through the private process environment. The real
S3 tests use a unique `dex-web-integration/<uuid>/` prefix and remove all versions
only inside that prefix. They exercise configuration before FDG, OAuth through
a TLS provider, Web restart, PKCE, actor and scope rejection, competing callbacks,
credential replacement before dispatch, uncertain exchange fencing, immutable
secret-free snapshots, API-key setup, generic trigger field constraints,
duration normalization, stale revision rejection, and manifest changes during
provider exchange. A subsequent callback recovers without another provider
exchange; competing conditional writes are reported as conflicts. The provider fixture uses
synthetic credentials and explicit certificate trust.

Local tests are not a complete application browser E2E receipt. Before a test
platform release, run the complete Local Kind project, Preview, build, Live,
configuration, OAuth, refresh, and cleanup paths with accepted source/image
identities. No hosted test deployment or external publication is claimed here.

## Documentation and dependency release status

This protocol and the Web README replace the removed hosted broker contract.
The shared `sdkgo/projectconfig` package and declarative trigger schema are local
unreleased source changes. Development uses an external, uncommitted Go
workspace. No unpublished pseudo-version or local replacement is added to a
committed module. Standalone reproducible builds require a published Connector
SDK version and newly published connector releases containing required trigger
schemas; existing Stripe v0.2.2 does not contain that schema. Dex's developer
skill baseline can advance only after a published Dex release exists.

## Local implementation receipt

The isolated Dex worktree is based on `efa3d975ea8be6b4f83dc1f3cf2cf4f631f135fc`.
Its external integration workspace is `/private/tmp/sv2-dex-project-config.go.work`,
using the isolated Connector SDK source worktree at
`/private/tmp/sv2-connectors-project-config`. Neither worktree has been published.

On 2026-09-29, `npm run check` passed TypeScript and 322 tests in 38 files;
`npm run build` passed. `go vet ./web/...` and `make copyright-check` passed.
The final `go test -race ./... -count=1` from `web/`, with real owned MinIO storage
and explicitly trusted TLS fixtures enabled, passed all Web packages including
six project-configuration integration scenarios. The Web package took 5.067s.
The only frontend build warning was the existing large-bundle size warning.

These results verify the local source protocol. They do not establish a complete
Kind business browser path, AWS deployment, published dependency, or image pin.

## Application environment

`application.environment` in the exact source AppManifest declares bounded
string fields with `name`, `required`, `secret`, `minLength`, and `enum`. It has no
defaults. Empty declarations preserve the previous environment contract digest;
nonempty declarations are sorted by name and encode all five keys. Enum values
are unique and sorted; secret fields cannot declare enums. MinLength counts
Unicode code points. Values are valid UTF-8 without NUL and at most 32,768 bytes.
At most 128 names and 128 enum values per name are supported. SDK, deployment,
AWS, trust, loader and proxy variables cannot be declared.

The native Connections page includes an Application environment editor even
when the application has no connectors and no Release/FDG exists. Ordinary
values are visible. Secret inputs are write-only and remain blank after saving;
leaving an input untouched retains its accepted reference. A changed empty input
is explicit and follows its declaration. Removal requires an explicit checkbox.
Obsolete declarations can be removed without exposing their stored values.

`GET /api/v2/application-environment` requires the same authenticated actor and
trusted forwarding context as project connector reads. Its response contains
`appManifestRevision`, `configurationRevision`, `csrfToken`, `fields`, and
`obsolete`. Each field contains its declaration, `configured`, and an ordinary
`value` only where permitted. No secret value, object reference, digest, or
storage target is returned.

`PUT /api/v2/application-environment` requires actor, matching Origin and CSRF,
`X-Dex-App-Manifest-Revision`, and `X-Dex-Configuration-Revision`. Its strict JSON
body has `values: map<string,string>`, `secrets: map<string,string>`, and
`remove: string[]`. Omitted fields remain unchanged; no name may appear in two
operations. Browser-supplied secret references are rejected. The body limit is
one MiB. Missing required entries may remain a draft, but internal validation
rejects readiness until all declarations and exact secret versions are valid.

A secret candidate is created under the fixed scope's `app-secrets/<NAME>/<id>`
path before the existing configuration CAS. Unknown writes reconcile the same
identity. Manifest changes before or during save produce a revision conflict;
readiness always validates the exact manifest/configuration pair again. Failed
CAS candidates and historical secret versions remain scope-owned because older
accepted deployments may reference them. Live and Preview prefixes never share
private references. Deleting Live serving keeps application data; final scope
deletion removes all versions.

Only the exact public environment path is needed by the authenticated BFF proxy.
The internal `/api/v2/project-configuration/app-manifest` and `/validate` remain
excluded from browser proxy admission. The application reads accepted values
through the shared SDK startup helper before reading environment settings or
starting application clients. No platform-specific fallback is supplied.

### Environment implementation receipt

On 2026-09-29, `npm run check` passed 325 tests in 39 files and TypeScript;
`npm run build` passed with the existing large-chunk warning. The full Web Go
race suite passed with real versioned S3 enabled, and `go vet ./web/...` and
`make copyright-check` passed. The additional environment integration verifies
unknown accepted writes, competing replicas, stale revision and manifest changes
during CAS, forbidden process settings, origin/CSRF, scope boundaries, and actual
SDK startup reading a prior accepted secret version after replacement.

`TestProjectApplicationEnvironmentBrowser` passed with actual Chrome against the
built native Web assets and real versioned S3, without any Release or Flow
Definition. It saved both ordinary and private values, reloaded, checked the
private input stayed blank, and confirmed internal readiness. Its authenticated
embedding boundary is an explicit fixture, so it does not claim full platform
BFF, hosted IAM/KMS, or complete application acceptance.

Reproduce from `web/` after `npm run build`, supplying the existing isolated S3
fixture through `DEX_PROJECT_CONFIG_TEST_ENDPOINT`,
`DEX_PROJECT_CONFIG_TEST_BUCKET`, and standard AWS credentials. Set
`DEX_PROJECT_CONFIG_PLAYWRIGHT_MODULE` to an installed Playwright `index.mjs`
and optionally `DEX_PROJECT_CONFIG_CHROMIUM` to an installed Chrome binary.
Run `go test -race ./... -count=1`; the browser test skips only when the explicit
Playwright module variable is absent. Development currently uses the external
workspace `/private/tmp/sv2-environment.work` to select unreleased Connector SDK
source. No module version pin or developer-skill release baseline was advanced.

The exact canonical fixture in the integration test hashes to
`sha256:607bf9a59849a614ac8c45886c3e4ff3cba7c4066fe4896f103943b5e02ca2e5`:
port 8080, health path `/healthz`, no connectors, required ordinary `APP_ENV`
with sorted enum `production, testing`, and required private `SIGNING_SECRET`
with minimum length 32. Both declarations encode all five canonical keys.

Logs: `/private/tmp/sv2-app-environment-web-check.log`,
`/private/tmp/sv2-app-environment-web-build.log`,
`/private/tmp/sv2-app-environment-dex-check.log`,
`/private/tmp/sv2-app-environment-dex-integration.log`,
`/private/tmp/sv2-app-environment-browser.log`, and
`/private/tmp/sv2-app-environment-dex-vet.log`.

Native manifest PUT and configuration validation responses include exact `scope`
and current `configurationRevision`, matching GET metadata. The storage
integration checks both fields at empty and existing configuration revisions;
platform adapters can verify the fixed boundary without inventing a scope.
