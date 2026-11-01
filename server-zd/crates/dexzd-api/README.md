# dexzd-api

## Purpose

FlowService and AdminService gRPC handlers for server-zd: request validation, routing to the engine, long polling, error mapping, and public API authentication.

## AdminService

`DexAdminService` serves `dex.AdminService`, the operator RPCs. It owns no node state. The server
injects an `ActiveNodeAdministration` implementation, and the handler maps its results to responses
and error statuses. `DexAdminService::authenticated_server` is the only public constructor, so the
service is always wrapped in admin authentication.

`DescribeActiveNode` calls `ActiveNodeAdministration::describe_active_node`. It returns the node ID,
node epoch, and holder process ID of the lease that the receiving process holds, and changes nothing.

`StepDownActiveNode` validates the request's optional `expected_node_id` and `expected_node_epoch`.
An empty `expected_node_id` ends with `INVALID_ARGUMENT`. The handler passes both as a
`StepDownPrecondition` to `ActiveNodeAdministration::step_down_active_node`. The node's lifecycle
owner, the serve loop, performs the step down; the trait future only submits it and awaits the
result. So a step down is ordered with a graceful SIGTERM shutdown. The serve loop checks the
precondition under the guard that starts the step down. It then stops serving, waits within a bound
for in-flight commits, releases the node lease, and keeps the process running as a standby. It waits
for a hold-off before it competes for the lease again. Meanwhile it watches the lease and reports
which process holds it:

- another process, as soon as it sees one take over;
- this process, when it takes the lease back after the hold-off;
- unknown, when neither is confirmed.

A call therefore lasts at most the commit drain bound, the release, the hold-off, and one lease
acquisition attempt. The handler awaits the trait future in a task of its own, so a client deadline
or disconnect never drops a submitted step down, and the result is still logged. Stepping down must
not send GOAWAY on, or close, the connection that carries the call.

| Error | gRPC status | Process state afterward |
|---|---|---|
| `ActiveNodeDescriptionError::NotActive` | `FAILED_PRECONDITION` | Unchanged |
| Empty `expected_node_id` | `INVALID_ARGUMENT` | Unchanged; the trait is not called |
| `ActiveNodeStepDownError::NotActive` | `FAILED_PRECONDITION` | Unchanged |
| `ActiveNodeStepDownError::PreconditionMismatch` | `FAILED_PRECONDITION` | Unchanged |
| `ActiveNodeStepDownError::AlreadySteppingDown` | `FAILED_PRECONDITION` | The earlier step down continues |
| `ActiveNodeStepDownError::LeaseReleaseUnconfirmed` | `INTERNAL` | Not serving and no longer renewing the lease, which may stay held until it expires; continues as a standby |
| `ActiveNodeStepDownError::Internal` | `INTERNAL` | Not serving and no longer renewing the lease; the serve loop exits the process after a short grace period that lets the handler pass the error to the transport |
| The step down task panicked or was cancelled | `INTERNAL` | Unknown; the detail says so |

Every status carries a `ServiceErrorResponse` with sub-status `UNCATEGORIZED`. No failure is
`UNAVAILABLE`: a call reaches whichever process is active, so a retry would step down the process
that took over. A client that sees `DEADLINE_EXCEEDED`, `CANCELLED`, or `UNAVAILABLE` does not know
the outcome. It retries only with the same `expected_node_id` and `expected_node_epoch` that the
failed call carried, never with values that `DescribeActiveNode` returns afterward. If the step down
already happened, the retry ends with `FAILED_PRECONDITION` and changes nothing.

With an authenticator configured, an admin call needs a JWT whose issuer's `jwt` authenticator lists
one of the token's audiences in `admin_audiences`. Issuers without admin audiences and static tokens
never qualify. An issuer with admin audiences and empty `audiences` authorizes admin calls only, and
its tokens never pass public API authentication. The admin check reuses the issuers' key set caches.
A missing or invalid credential ends with `UNAUTHENTICATED`, and a valid public API credential
without an admin audience ends with `PERMISSION_DENIED`. The handler logs each authorized admin call
with the caller's `iss` and `sub`: successes at info level and step down failures at warn level.
Denied calls are logged at warn level, at most once every 10 seconds, with a count of the skipped
ones. Without an authenticator, every call passes, as on a development server, so anyone who can
reach the listener can step the node down.
