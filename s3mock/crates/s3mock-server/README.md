# s3mock-server

## Purpose

S3-compatible HTTP server that serves an s3mock store over path-style URLs.

## Design

`s3mock_server::start` serves a `tokio::net::TcpListener` and returns a `ServerHandle` for
graceful shutdown. Tests bind port 0 and read the chosen address from the handle.

`start_with_config` configures faults, request observations, and signature verification. In strict
mode, the root key signs admin requests and scoped keys are checked against their S3 policies.
Credential endpoints require the root signature in strict mode. Other test controls remain
unauthenticated unless `--control-authentication root` is selected.
Both header-signed requests and presigned URLs use the same policy checks.
Bucket CORS rules are persisted in the store and evaluated for unsigned OPTIONS preflights and
requests carrying Origin.
See the [workspace README](../../README.md#testing-controls) for flags, IAM actions, and the JSON
admin endpoint contract.
The admin dispatcher lives in `controls/admin.rs`, separately from the S3 router, for additional controls.
