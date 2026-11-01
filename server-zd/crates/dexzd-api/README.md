# dexzd-api

## Purpose

FlowService and AdminService gRPC handlers for server-zd: request validation, routing to the engine, long polling, error mapping, and public API authentication.

## AdminService

`DexAdminService` serves `dex.AdminService`, the operator RPCs. It owns no node state. The server
injects an `ActiveNodeStepDown` implementation at construction, and the handler maps its result to
the response and error statuses.

`StepDownActiveNode` calls `ActiveNodeStepDown::step_down_active_node`. The implementation stops
serving, waits within a bound for in-flight commits, releases the node lease, and keeps the process
running as a standby. It waits for a hold-off before it competes for the lease again. It returns
once the lease is released, with any takeover by another process that it observed. Dropping the
future, as a cancelled call does, must not interrupt a step down that has started.

| `ActiveNodeStepDownError` | gRPC status |
|---|---|
| `NotActive` | `FAILED_PRECONDITION` |
| `AlreadySteppingDown` | `FAILED_PRECONDITION` |
| `LeaseReleaseUnconfirmed` | `INTERNAL`; the detail says that the lease stays held until it expires |
| `Internal` | `INTERNAL` |

Every status carries a `ServiceErrorResponse` with sub-status `UNCATEGORIZED`. No failure is
`UNAVAILABLE`: a call reaches whichever process is active, so a retry would step down the process
that took over.

Wrap the service with `AuthenticatedService::for_admin_service`. With an authenticator configured,
a call needs a JWT that a configured issuer accepts and whose `aud` names an entry of
`auth.adminAudiences`. Static tokens never qualify. The admin check reuses the issuers' key set
caches. A missing or invalid credential ends with `UNAUTHENTICATED`, and a valid public API
credential without an admin audience ends with `PERMISSION_DENIED`. Without an authenticator, every
call passes, as on a development server.
