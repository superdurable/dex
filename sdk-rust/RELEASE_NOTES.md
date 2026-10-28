# Rust SDK release notes

## Unreleased

- **Breaking change:** Removed push Workers and the public `WorkerTarget` type, including `new`, `pull`, `headless`, `address`, `is_headless`, and `is_pull`. Removed `WorkerOptions::bind_address`, `WorkerOptions::worker_target`, `Worker::worker_target`, `ClientOptions::worker_target`, and `FlowConfig::worker_target`. Delete these target and listener settings when migrating. All Workers now use pull and need only an outbound connection to the Dex Server. `WorkerOptions::new()` uses pull defaults; keep `WorkerOptions::pull(PullWorkerOptions::new()...)` when tuning capacity or lifecycle. This change ships in the next minor SDK release.

- **Routing migration:** StartFlow always sends a pull Worker target instead of using an operator-configured push default. SubFlow always sends pull instead of inheriting a parent push target. UpdateFlowConfig always changes the Worker target to pull, even when only another field is supplied. Other omitted fields retain their existing default, inheritance, or update behavior. Move affected Flow types to pull Workers before starting or updating them with this SDK. Applications that still require push routing must keep their previous SDK version until they migrate.

- Flow close cleanup now returns `ErrorSubStatus::FlowClosing` and `SdkError::FlowClosing`. These additions change exhaustive matches on both public enums. Add a wildcard arm to existing matches and handle `SdkError::FlowClosing` where code previously expected `SdkError::Service` with `FAILED_PRECONDITION`. Both enums are now non-exhaustive for future additions.
