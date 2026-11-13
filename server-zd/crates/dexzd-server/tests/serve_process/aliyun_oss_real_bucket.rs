// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Manual checks against a real Aliyun OSS bucket; see `crates/dexzd-server/README.md`.

use std::fs;
use std::panic::resume_unwind;
use std::path::Path;
use std::sync::Arc;

use bytes::Bytes;
use dexzd_metrics::NodeMetrics;
use dexzd_object_store::{
    AliyunOssObjectStore, InstrumentedObjectStore, MANIFEST_OBJECT_KEY, ObjectStore, S3ApiDialect,
    S3Credentials, S3ObjectStore, S3ObjectStoreConfig, WriteCondition, WriteOutcome,
    check_aliyun_oss_create_only_writes, check_no_data_of_another_provider, startup_self_check,
};
use dexzd_protocol::dex::GetFlowSummaryRequest;
use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use tempfile::TempDir;

use super::harness::{ServeProcess, free_loopback_address};
use super::{NODE_ID, SHUTDOWN_BUDGET, run_greeting_flow, start_greeting_worker};

const MANUAL_TEST_DOC: &str = "crates/dexzd-server/README.md";
const MULTIPART_FILE_BYTES: usize = 20 * 1024 * 1024;
const PART_BYTES: usize = 8 * 1024 * 1024;
const DELETE_BATCH_KEYS: usize = 1000;

/// The real bucket that the environment names.
#[derive(Clone)]
struct RealBucket {
    endpoint: String,
    region: String,
    bucket: String,
    access_key_id: String,
    secret_access_key: String,
    prefix: String,
}

impl RealBucket {
    /// Reads every `DEX_OSS_*` variable and panics naming each one that is missing.
    fn from_environment() -> Self {
        let names = [
            "DEX_OSS_ENDPOINT",
            "DEX_OSS_REGION",
            "DEX_OSS_BUCKET",
            "DEX_OSS_ACCESS_KEY_ID",
            "DEX_OSS_SECRET_ACCESS_KEY",
        ];
        let values: Vec<Option<String>> = names
            .iter()
            .map(|name| std::env::var(name).ok().filter(|value| !value.is_empty()))
            .collect();
        let missing: Vec<&str> = names
            .iter()
            .zip(&values)
            .filter(|(_, value)| value.is_none())
            .map(|(name, _)| *name)
            .collect();
        assert!(
            missing.is_empty(),
            "set {} to run this manual test; see {MANUAL_TEST_DOC}",
            missing.join(", ")
        );
        let mut values = values
            .into_iter()
            .map(|value| value.expect("checked above"));
        let mut next = || values.next().expect("one value per name");
        let nanos = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .expect("system clock is after the Unix epoch")
            .as_nanos();
        Self {
            endpoint: next(),
            region: next(),
            bucket: next(),
            access_key_id: next(),
            secret_access_key: next(),
            prefix: format!("dexzd-manual-test/{nanos}"),
        }
    }

    /// Returns the dialect store under the test prefix, which sees physical keys.
    async fn physical_store(&self) -> S3ObjectStore {
        let mut config = S3ObjectStoreConfig::new(self.region.clone(), self.bucket.clone());
        config.endpoint = Some(self.endpoint.clone());
        config.key_prefix = self.prefix.clone();
        config.credentials = S3Credentials::Static {
            access_key_id: self.access_key_id.clone(),
            secret_access_key: self.secret_access_key.clone(),
            session_token: None,
        };
        config.multipart_part_size = PART_BYTES;
        config.multipart_threshold = PART_BYTES;
        config.api_dialect = S3ApiDialect::AliyunOss;
        S3ObjectStore::new(config)
            .await
            .expect("the Aliyun OSS store is configured")
    }

    /// Deletes every physical object under the test prefix.
    async fn delete_prefix(&self) {
        let store = self.physical_store().await;
        loop {
            let listing = store
                .list("", None, 1000)
                .await
                .expect("the test prefix lists");
            if listing.objects.is_empty() {
                eprintln!("deleted every object under {}/", self.prefix);
                return;
            }
            let keys: Vec<String> = listing
                .objects
                .into_iter()
                .map(|object| object.key)
                .collect();
            store
                .delete_many(&keys)
                .await
                .expect("the test objects are deleted");
        }
    }

    /// Runs `checks`, then deletes the test prefix, then resumes a panic of `checks`.
    async fn run_then_delete_prefix<F>(self, checks: impl FnOnce(Self) -> F)
    where
        F: Future<Output = ()> + Send + 'static,
    {
        eprintln!("writing under {}/{}/", self.bucket, self.prefix);
        let outcome = tokio::spawn(checks(self.clone())).await;
        self.delete_prefix().await;
        if let Err(error) = outcome {
            resume_unwind(error.into_panic());
        }
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
#[ignore = "needs a real Aliyun OSS bucket; run manually, see crates/dexzd-server/README.md"]
async fn real_aliyun_oss_bucket_passes_the_object_store_checks() {
    RealBucket::from_environment()
        .run_then_delete_prefix(check_object_store)
        .await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
#[ignore = "needs a real Aliyun OSS bucket; run manually, see crates/dexzd-server/README.md"]
async fn real_aliyun_oss_bucket_serves_and_recovers_after_a_restart() {
    RealBucket::from_environment()
        .run_then_delete_prefix(check_serve_and_restart)
        .await;
}

async fn check_object_store(bucket: RealBucket) {
    let inner: Arc<dyn ObjectStore> = Arc::new(bucket.physical_store().await);
    check_no_data_of_another_provider(inner.as_ref())
        .await
        .expect("the new prefix holds no other provider's data");
    check_aliyun_oss_create_only_writes(inner.as_ref())
        .await
        .expect("the bucket honors x-oss-forbid-overwrite");
    eprintln!("PASS: create-only writes");
    let adapter = Arc::new(AliyunOssObjectStore::new(
        Arc::clone(&inner),
        dexzd_metrics::NodeMetrics::unregistered(),
    ));
    adapter
        .remove_stale_startup_probes()
        .await
        .expect("stale probes are removed by store time");
    let store = InstrumentedObjectStore::new(adapter, NodeMetrics::unregistered());
    startup_self_check(&store)
        .await
        .expect("the startup self check passes through the adapter");
    eprintln!("PASS: startup self check");

    let created = store
        .put(
            MANIFEST_OBJECT_KEY,
            Bytes::from_static(b"generation 1"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("the manifest is created");
    let WriteOutcome::Written { etag: first_etag } = created else {
        panic!("the first manifest write must succeed: {created:?}");
    };
    let replaced = store
        .put(
            MANIFEST_OBJECT_KEY,
            Bytes::from_static(b"generation 2"),
            WriteCondition::ReplaceIfEtagMatches {
                etag: first_etag.clone(),
            },
        )
        .await
        .expect("the manifest is replaced");
    assert!(
        matches!(replaced, WriteOutcome::Written { .. }),
        "{replaced:?}"
    );
    let stale = store
        .put(
            MANIFEST_OBJECT_KEY,
            Bytes::from_static(b"stale"),
            WriteCondition::ReplaceIfEtagMatches { etag: first_etag },
        )
        .await
        .expect("a stale replacement reaches the store");
    assert_eq!(stale, WriteOutcome::PreconditionFailed);
    let manifest = store
        .get(MANIFEST_OBJECT_KEY)
        .await
        .expect("the manifest reads")
        .expect("the manifest exists");
    assert_eq!(manifest.bytes, Bytes::from_static(b"generation 2"));
    let listing = store.list("", None, 1000).await.expect("the prefix lists");
    assert!(
        listing
            .objects
            .iter()
            .all(|object| !object.key.contains(".v/")),
        "logical listings hide versions: {:?}",
        listing.objects
    );
    eprintln!("PASS: versioned compare-and-swap");

    let directory = TempDir::new().expect("a temporary directory is created");
    let file = directory.path().join("multipart");
    let body: Vec<u8> = (0..MULTIPART_FILE_BYTES)
        .map(|index| u8::try_from(index % 251).expect("below 251"))
        .collect();
    fs::write(&file, &body).expect("the multipart file is written");
    let written = store
        .put_file("files/multipart", &file, WriteCondition::CreateOnly)
        .await
        .expect("the multipart file uploads");
    assert!(
        matches!(written, WriteOutcome::Written { .. }),
        "{written:?}"
    );
    let downloaded = directory.path().join("downloaded");
    store
        .get_to_file("files/multipart", &downloaded)
        .await
        .expect("the multipart file downloads")
        .expect("the multipart file exists");
    assert_eq!(fs::read(&downloaded).expect("the download reads"), body);
    eprintln!("PASS: multipart file upload");

    let keys: Vec<String> = (0..DELETE_BATCH_KEYS)
        .map(|index| format!("batch/{index:04}"))
        .collect();
    for key in &keys {
        store
            .put(key, Bytes::from_static(b"x"), WriteCondition::CreateOnly)
            .await
            .expect("a batch object is created");
    }
    store
        .delete_many(&keys)
        .await
        .expect("DeleteObjects deletes the batch");
    let remaining = store
        .list("batch/", None, 10)
        .await
        .expect("the batch lists");
    assert!(remaining.objects.is_empty(), "{:?}", remaining.objects);
    eprintln!("PASS: batch delete");
}

async fn check_serve_and_restart(bucket: RealBucket) {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let local_directory = directory.path().join("store");
    let flow_id = format!("real-aliyun-oss-{NODE_ID}");
    let mut first_run_id = String::new();
    for restart in 0..2 {
        let address = free_loopback_address();
        let config = directory.path().join(format!("serve-{restart}.yaml"));
        write_real_bucket_config(&config, &bucket, &local_directory, address);
        let mut process = ServeProcess::spawn_with_static_keys(
            &config,
            address,
            directory.path().join(format!("serve-{restart}.log")),
            &bucket.access_key_id,
            &bucket.secret_access_key,
        );
        let channel = process.wait_until_serving().await;
        let mut flow_client = FlowServiceClient::new(channel.clone());
        if restart == 0 {
            let worker = start_greeting_worker(channel).await;
            first_run_id = run_greeting_flow(&mut flow_client, &flow_id).await;
            worker.stop().await;
        }
        let summary = flow_client
            .get_flow_summary(GetFlowSummaryRequest {
                flow_id: flow_id.clone(),
                run_id: String::new(),
            })
            .await
            .unwrap_or_else(|error| panic!("GetFlowSummary failed: {error}; {}", process.log()))
            .into_inner();
        assert_eq!(summary.first_run_id, first_run_id, "{}", process.log());
        let status = process.terminate(SHUTDOWN_BUDGET).await;
        assert_eq!(status.code(), Some(0), "{}", process.log());
        eprintln!("PASS: serve start {restart}");
    }
}

fn write_real_bucket_config(
    path: &Path,
    bucket: &RealBucket,
    local_directory: &Path,
    listen: std::net::SocketAddr,
) {
    let contents = format!(
        "cluster:\n  nodeId: {NODE_ID}\n  partitionCount: 16\nobjectStore:\n  provider: aliyunOSS\n  endpoint: {endpoint}\n  region: {region}\n  bucket: {name}\n  prefix: {prefix}\n  credentialsSource: environment\nlocalStorage:\n  directory: {directory}\n  blockCacheBytes: 16777216\nlease:\n  duration: 30s\n  failIfHeld: true\napi:\n  listen: \"{listen}\"\nsearch:\n  backend: builtin\n",
        endpoint = bucket.endpoint,
        region = bucket.region,
        name = bucket.bucket,
        prefix = bucket.prefix,
        directory = local_directory.display(),
    );
    fs::write(path, contents).expect("the serve configuration is written");
}
