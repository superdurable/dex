# s3mock-conformance

## Purpose

S3 conformance tests that run through aws-sdk-s3 against s3mock or real S3.

Presigned URL cases use the SDK to sign GET and PUT requests, then send them with `curl` so the
request is not signed again by the SDK. `curl` must be on `PATH` when running the suite.
