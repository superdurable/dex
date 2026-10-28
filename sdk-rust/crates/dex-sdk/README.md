# dex-sdk

## Purpose

Strongly typed Rust client and worker APIs for durable Dex Flows.

## Workers

Workers use pull task streams and need only an outbound connection to the Dex
Server. `WorkerOptions::new()` uses pull defaults; `WorkerOptions::pull` tunes
capacity and lifecycle through `PullWorkerOptions`. Clients route Flows and
SubFlows to pull Workers automatically.

## API key credentials

When Dex requires access tokens and an application holds an API key, create an
`ApiKeyCredential` before constructing a Client or Worker. Supply the token
endpoint URL and the Dex server ID explicitly. Use `FileCredential` when another
process already writes access tokens to a local file.

```rust
use std::{sync::Arc, time::Duration};
use dex_sdk::{ApiKeyCredential, ClientOptions, WorkerOptions};

let runtime = tokio::runtime::Runtime::new()?;
let credential = Arc::new(
    runtime.block_on(ApiKeyCredential::builder(token_url, server_id, api_key)
        .exchange_timeout(Duration::from_secs(10))
        .build())?,
);
let client_options = ClientOptions::new().credential(credential.clone());
let worker_options = WorkerOptions::new().credential(credential);
```

Construction exchanges the key once and fails if the endpoint rejects it.
The URL must use HTTPS, except HTTP is allowed on localhost and loopback IP
addresses. Each exchange has a ten-second timeout by default. A background task
refreshes at 80% of the token lifetime, shares one exchange across callers, and
keeps serving a valid token if refresh fails. An expired token fails calls until
refresh succeeds. After Dex rejects a unary call, the SDK refreshes and retries
it once. Rejected Worker poll streams reconnect once immediately with a fresh
token; a second rejection uses the normal Worker backoff.
The application owns the credential and may share it among Clients and Workers.
Dropping a Client or Worker releases only its reference; dropping the last owner
aborts the refresh task. Keep the runtime alive and driving tasks while the
credential is in use. A multi-thread runtime does this while it remains alive;
a current-thread runtime must be continuously driven. An injected reqwest
client builder can configure proxies or private trust roots; the credential
disables redirects before building it. The default client uses bundled webpki
trust roots. Credential formatting and exchange errors never
reveal the API key or an access token.
