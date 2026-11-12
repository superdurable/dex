# dexzd-server

## Purpose

The dex-server-zd executable: typed configuration, startup, shutdown, the serve mode for external object storage, and the dev mode with an embedded S3 emulator.

## Manual Aliyun OSS tests

`tests/serve_process/aliyun_oss_real_bucket.rs` checks `objectStore.provider: aliyunOSS`
against a real Aliyun OSS bucket. CI never runs these tests. Run them after changing aliyunOSS
code, after upgrading aws-sdk, and before a release.

The bucket must never have had versioning enabled. Each test writes under a new prefix,
`dexzd-manual-test/<unix-nanos>/`, and deletes every object under it at the end, also when a
check fails. From `server-zd/`:

```text
DEX_OSS_ENDPOINT=https://s3.oss-cn-hangzhou.aliyuncs.com \
DEX_OSS_REGION=cn-hangzhou \
DEX_OSS_BUCKET=<bucket> \
DEX_OSS_ACCESS_KEY_ID=<access key ID> \
DEX_OSS_SECRET_ACCESS_KEY=<secret access key> \
cargo test -p dexzd-server --test serve_process real_aliyun_oss -- --ignored --nocapture --test-threads=1
```

With `--ignored`, a missing variable fails the test and names every missing variable.
