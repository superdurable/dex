// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Manual checks against a real Tencent COS bucket; see `crates/dexzd-server/README.md`.

use std::fs;
use std::panic::resume_unwind;
use std::path::Path;
use std::sync::Arc;

use bytes::Bytes;
use dexzd_metrics::NodeMetrics;
use dexzd_object_store::{
    AliyunOssObjectStore, InstrumentedObjectStore, MANIFEST_OBJECT_KEY, ObjectStore, S3ApiDialect,
    S3Credentials, S3ObjectStore, S3ObjectStoreConfig, WriteCondition, WriteOutcome,
    check_tencent_cos_bucket_versioning, check_tencent_cos_create_only_writes,
    check_tencent_cos_holds_no_data_of_another_provider, startup_self_check,
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
const CONCURRENT_APPEND_ROUNDS: usize = 5;
const CONCURRENT_APPEND_WRITERS: usize = 16;

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
    /// Reads every `DEX_COS_*` variable and panics naming each one that is missing.
    fn from_environment() -> Self {
        let names = [
            "DEX_COS_ENDPOINT",
            "DEX_COS_REGION",
            "DEX_COS_BUCKET",
            "DEX_COS_ACCESS_KEY_ID",
            "DEX_COS_SECRET_ACCESS_KEY",
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
        config.api_dialect = S3ApiDialect::TencentCos;
        S3ObjectStore::new(config)
            .await
            .expect("the Tencent COS store is configured")
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
#[ignore = "needs a real Tencent COS bucket; run manually, see crates/dexzd-server/README.md"]
async fn real_tencent_cos_bucket_passes_the_object_store_checks() {
    RealBucket::from_environment()
        .run_then_delete_prefix(check_object_store)
        .await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
#[ignore = "needs a real Tencent COS bucket; run manually, see crates/dexzd-server/README.md"]
async fn real_tencent_cos_bucket_serves_and_recovers_after_a_restart() {
    RealBucket::from_environment()
        .run_then_delete_prefix(check_serve_and_restart)
        .await;
}

async fn check_object_store(bucket: RealBucket) {
    let physical = bucket.physical_store().await;
    check_tencent_cos_bucket_versioning(&physical)
        .await
        .expect("the bucket never had versioning enabled");
    let inner: Arc<dyn ObjectStore> = Arc::new(physical);
    check_tencent_cos_holds_no_data_of_another_provider(inner.as_ref())
        .await
        .expect("the new prefix holds no other provider's data");
    check_tencent_cos_create_only_writes(inner.as_ref())
        .await
        .expect("the bucket rejects a second AppendObject at position 0");
    eprintln!("PASS: create-only writes");
    check_concurrent_appends(&inner).await;
    check_listing_matches_reads(&inner).await;
    let adapter = Arc::new(AliyunOssObjectStore::new(Arc::clone(&inner)));
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
        .put_file("files/multipart", &file, WriteCondition::Unconditional)
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
    let appended = store
        .put_file("files/appended", &file, WriteCondition::CreateOnly)
        .await
        .expect("the large create-only file appends");
    assert!(
        matches!(appended, WriteOutcome::Written { .. }),
        "{appended:?}"
    );
    let downloaded_append = directory.path().join("downloaded-append");
    store
        .get_to_file("files/appended", &downloaded_append)
        .await
        .expect("the appended file downloads")
        .expect("the appended file exists");
    assert_eq!(
        fs::read(&downloaded_append).expect("the download reads"),
        body
    );
    assert_eq!(
        store
            .put_file("files/appended", &file, WriteCondition::CreateOnly)
            .await
            .expect("a duplicate append reaches the bucket"),
        WriteOutcome::PreconditionFailed
    );
    eprintln!("PASS: large create-only file append");

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
    // COS still lists a key for tens of milliseconds after its delete, so reads decide.
    for key in &keys {
        assert!(
            store
                .head(key)
                .await
                .expect("a deleted batch object heads")
                .is_none(),
            "{key} is gone"
        );
    }
    eprintln!("PASS: batch delete");
}

async fn check_serve_and_restart(bucket: RealBucket) {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let local_directory = directory.path().join("store");
    let flow_id = format!("real-tencent-cos-{NODE_ID}");
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
        "cluster:\n  nodeId: {NODE_ID}\n  partitionCount: 16\nobjectStore:\n  provider: tencentCOS\n  endpoint: {endpoint}\n  region: {region}\n  bucket: {name}\n  prefix: {prefix}\n  credentialsSource: environment\nlocalStorage:\n  directory: {directory}\n  blockCacheBytes: 16777216\nlease:\n  duration: 30s\n  failIfHeld: true\napi:\n  listen: \"{listen}\"\nsearch:\n  backend: builtin\n",
        endpoint = bucket.endpoint,
        region = bucket.region,
        name = bucket.bucket,
        prefix = bucket.prefix,
        directory = local_directory.display(),
    );
    fs::write(path, contents).expect("the serve configuration is written");
}

/// Many writers append the same new key at once; exactly one may win each round.
async fn check_concurrent_appends(inner: &Arc<dyn ObjectStore>) {
    for round in 0..CONCURRENT_APPEND_ROUNDS {
        let key = format!("race/{round}");
        let mut creators = tokio::task::JoinSet::new();
        for creator in 0..CONCURRENT_APPEND_WRITERS {
            let inner = Arc::clone(inner);
            let key = key.clone();
            creators.spawn(async move {
                let body = Bytes::from(format!("creator-{creator}"));
                let outcome = inner
                    .put(&key, body.clone(), WriteCondition::CreateOnly)
                    .await
                    .expect("a concurrent append reaches a definite outcome");
                (body, outcome)
            });
        }
        let mut winners = Vec::new();
        while let Some(joined) = creators.join_next().await {
            let (body, outcome) = joined.expect("a creator task completes");
            if matches!(outcome, WriteOutcome::Written { .. }) {
                winners.push(body);
            }
        }
        assert_eq!(winners.len(), 1, "round {round} has exactly one winner");
        let content = inner
            .get(&key)
            .await
            .expect("the winner reads")
            .expect("the winner exists");
        assert_eq!(content.bytes, winners[0]);
    }
    eprintln!("PASS: concurrent appends");
}

/// The versioned layer needs one identity for an appended object in LIST, GET, and range GET.
async fn check_listing_matches_reads(inner: &Arc<dyn ObjectStore>) {
    let key = "identity/appended";
    let written = inner
        .put(
            key,
            Bytes::from_static(b"appended object body"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("the object appends");
    let WriteOutcome::Written { etag: written_etag } = written else {
        panic!("a new key appends: {written:?}");
    };
    let listed = inner
        .list(key, None, 1)
        .await
        .expect("the object lists")
        .objects
        .remove(0);
    let whole = inner
        .get(key)
        .await
        .expect("the object reads")
        .expect("the object exists");
    let range = inner
        .get_range(
            key,
            dexzd_object_store::ByteRange {
                start: 0,
                end_inclusive: 3,
            },
        )
        .await
        .expect("the range reads")
        .expect("the object exists");
    assert_eq!(
        written_etag.trim_matches('"'),
        whole.etag.trim_matches('"'),
        "the append reports the ETag that reads return"
    );
    assert_eq!(listed.etag.trim_matches('"'), whole.etag.trim_matches('"'));
    assert_eq!(listed.etag.trim_matches('"'), range.etag.trim_matches('"'));
    assert_eq!(listed.size, whole.object_size);
    eprintln!("PASS: listing and reads name one object");
    // A rewritten history segment is deleted, then created again at once.
    inner.delete(key).await.expect("the object is deleted");
    let recreated = inner
        .put(
            key,
            Bytes::from_static(b"recreated body"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("the recreate reaches the bucket");
    assert!(
        matches!(recreated, WriteOutcome::Written { .. }),
        "an append right after a delete creates the key: {recreated:?}"
    );
    eprintln!("PASS: create right after delete");
}
