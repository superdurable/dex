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

`StepDownActiveNode` calls `ActiveNodeAdministration::step_down_active_node` with the request's
optional `expected_node_epoch`. The implementation compares that epoch under the guard that starts
the step down. It then stops serving, waits within a bound for in-flight commits, releases the node
lease, and keeps the process running as a standby. It waits for a hold-off before it competes for
the lease again. Meanwhile it watches the lease and reports which process holds it:

- another process, as soon as it sees one take over;
- this process, when it takes the lease back after the hold-off;
- unknown, when neither is confirmed.

A call therefore lasts at most the commit drain bound, the release, the hold-off, and one lease
acquisition attempt. The handler runs the step down in a task of its own and awaits its
`JoinHandle`, so a client deadline or disconnect never interrupts a started step down. Stepping down
must not send GOAWAY on, or close, the connection that carries the call.

| Error | gRPC status | Process state afterward |
|---|---|---|
| `ActiveNodeDescriptionError::NotActive` | `FAILED_PRECONDITION` | Unchanged |
| `ActiveNodeStepDownError::NotActive` | `FAILED_PRECONDITION` | Unchanged |
| `ActiveNodeStepDownError::NodeEpochMismatch` | `FAILED_PRECONDITION` | Unchanged |
| `ActiveNodeStepDownError::AlreadySteppingDown` | `FAILED_PRECONDITION` | The earlier step down continues |
| `ActiveNodeStepDownError::LeaseReleaseUnconfirmed` | `INTERNAL` | Not serving and no longer renewing the lease, which may stay held until it expires; continues as a standby |
| `ActiveNodeStepDownError::Internal` | `INTERNAL` | Not serving and no longer renewing the lease; exits after returning the error |

Every status carries a `ServiceErrorResponse` with sub-status `UNCATEGORIZED`. No failure is
`UNAVAILABLE`: a call reaches whichever process is active, so a retry would step down the process
that took over. A client that sees `DEADLINE_EXCEEDED`, `CANCELLED`, or `UNAVAILABLE` does not know
the outcome. It calls `DescribeActiveNode` first and retries only with `expected_node_epoch`.

With an authenticator configured, an admin call needs a JWT whose issuer's `jwt` authenticator lists
one of the token's audiences in `admin_audiences`. Issuers without admin audiences and static tokens
never qualify. The admin check reuses the issuers' key set caches. A missing or invalid credential
ends with `UNAUTHENTICATED`, and a valid public API credential without an admin audience ends with
`PERMISSION_DENIED`. The handler logs each authorized admin call with the caller's `iss` and `sub`:
successes at info level and step down failures at warn level. Denied calls are logged at warn level, at most once every 10 seconds, with a count
of the skipped ones. Without an authenticator, every call passes, as on a development server, so
anyone who can reach the listener can step the node down.
