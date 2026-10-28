# Rust SDK changelog

## Unreleased

- **Breaking change:** Removed push Workers and the public `WorkerTarget` type, including `new`, `pull`, `headless`, `address`, `is_headless`, and `is_pull`. Removed `WorkerOptions::bind_address`, `WorkerOptions::worker_target`, `Worker::worker_target`, `ClientOptions::worker_target`, and `FlowConfig::worker_target`. Delete these target and listener settings when migrating. All Workers now use pull and need only an outbound connection to the Dex Server. `WorkerOptions::new()` uses pull defaults; keep `WorkerOptions::pull(PullWorkerOptions::new()...)` when tuning capacity or lifecycle. This change ships in the next minor SDK release.

See [release notes](RELEASE_NOTES.md) for additional changes.
