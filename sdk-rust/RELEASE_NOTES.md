# Rust SDK release notes

## Unreleased

- **Breaking change:** Removed push Workers and the public `WorkerTarget` type, including `new`, `pull`, `headless`, `address`, `is_headless`, and `is_pull`. Removed `WorkerOptions::bind_address`, `WorkerOptions::worker_target`, `Worker::worker_target`, `ClientOptions::worker_target`, and `FlowConfig::worker_target`. Delete these target and listener settings when migrating. All Workers now use pull and need only an outbound connection to the Dex Server. `WorkerOptions::new()` uses pull defaults; keep `WorkerOptions::pull(PullWorkerOptions::new()...)` when tuning capacity or lifecycle. This change ships in the next minor SDK release.

- Flow close cleanup now returns `ErrorSubStatus::FlowClosing` and `SdkError::FlowClosing`. These additions change exhaustive matches on both public enums. Add a wildcard arm to existing matches and handle `SdkError::FlowClosing` where code previously expected `SdkError::Service` with `FAILED_PRECONDITION`. Both enums are now non-exhaustive for future additions.
