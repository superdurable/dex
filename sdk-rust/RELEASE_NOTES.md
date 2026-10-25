# Rust SDK release notes

## Unreleased

- Flow close cleanup now returns `ErrorSubStatus::FlowClosing` and `SdkError::FlowClosing`. These additions change exhaustive matches on both public enums. Add a wildcard arm to existing matches and handle `SdkError::FlowClosing` where code previously expected `SdkError::Service` with `FAILED_PRECONDITION`. Both enums are now non-exhaustive for future additions.
