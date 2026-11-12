// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Runs the shared conformance suite against real Azure Blob Storage and Google Cloud Storage.
//!
//! Run manually after changing the backend, upgrading `object_store`, and before a release:
//!
//! ```bash
//! export DEX_AZURE_ACCOUNT=<account> DEX_AZURE_ACCESS_KEY=<key> DEX_AZURE_CONTAINER=<container>
//! export DEX_GCS_BUCKET=<bucket> DEX_GCS_SERVICE_ACCOUNT_PATH=<key file, or empty for ADC>
//! cargo test --manifest-path server-zd/Cargo.toml -p dexzd-object-store --test conformance \
//!     azure_gcs_cloud -- --ignored --nocapture
//! ```
//!
//! Each run writes under a unique prefix and deletes it afterwards.

use std::panic::AssertUnwindSafe;
use std::path::PathBuf;
use std::time::{SystemTime, UNIX_EPOCH};

use bytes::Bytes;
use dexzd_object_store::{
    AzureBlobCredentials, AzureGcsObjectStore, AzureGcsObjectStoreConfig, AzureGcsProvider,
    GoogleCloudStorageCredentials, ObjectStore, STAGING_KEY_PREFIX, WriteCondition, WriteOutcome,
};
use futures::FutureExt;

use super::check_store;

/// Larger than the 64 MiB single-request limit, so it takes the staging and copy path.
const STAGED_FILE_BYTES: usize = 130 * 1024 * 1024;

fn required_environment(name: &str) -> String {
    std::env::var(name).unwrap_or_else(|_| panic!("set {name} to run the real-cloud conformance"))
}

fn unique_prefix() -> String {
    format!(
        "dex-conformance/{}-{}",
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("the clock is after the epoch")
            .as_millis(),
        std::process::id()
    )
}

fn azure_provider() -> AzureGcsProvider {
    AzureGcsProvider::AzureBlob {
        account: required_environment("DEX_AZURE_ACCOUNT"),
        container: required_environment("DEX_AZURE_CONTAINER"),
        credentials: AzureBlobCredentials::AccountKey {
            account_key: required_environment("DEX_AZURE_ACCESS_KEY"),
        },
    }
}

fn google_cloud_storage_provider() -> AzureGcsProvider {
    let service_account_path = std::env::var("DEX_GCS_SERVICE_ACCOUNT_PATH")
        .ok()
        .filter(|path| !path.is_empty());
    AzureGcsProvider::GoogleCloudStorage {
        bucket: required_environment("DEX_GCS_BUCKET"),
        credentials: match service_account_path {
            Some(path) => GoogleCloudStorageCredentials::ServiceAccountKeyFile {
                path: PathBuf::from(path),
            },
            None => GoogleCloudStorageCredentials::ApplicationDefault {
                credentials_file: None,
            },
        },
    }
}

fn cloud_store(provider: AzureGcsProvider, prefix: &str) -> AzureGcsObjectStore {
    let mut config = AzureGcsObjectStoreConfig::new(provider);
    config.key_prefix = prefix.to_owned();
    AzureGcsObjectStore::new(config).expect("the cloud store builds")
}

#[tokio::test]
#[ignore = "needs a real Azure/GCS account; run manually"]
async fn azure_gcs_cloud_azure_blob_conformance() {
    run_cloud_conformance(azure_provider()).await;
}

#[tokio::test]
#[ignore = "needs a real Azure/GCS account; run manually"]
async fn azure_gcs_cloud_google_cloud_storage_conformance() {
    run_cloud_conformance(google_cloud_storage_provider()).await;
}

async fn run_cloud_conformance(provider: AzureGcsProvider) {
    let prefix = unique_prefix();
    let store = cloud_store(provider, &prefix);
    let checks = async {
        check_store(&store).await;
        check_etags_match_across_operations(&store).await;
        check_single_key_pages_walk_every_key(&store).await;
        check_staged_create_of_a_large_file(&store).await;
        let deleted = store
            .delete_abandoned_staging_objects()
            .await
            .expect("the staging cleanup succeeds");
        assert_eq!(
            deleted, 0,
            "a fresh prefix has no abandoned staging objects"
        );
    };
    let checked = AssertUnwindSafe(checks).catch_unwind().await;
    delete_everything(&store).await;
    if let Err(panic) = checked {
        std::panic::resume_unwind(panic);
    }
}

async fn check_etags_match_across_operations(store: &AzureGcsObjectStore) {
    let WriteOutcome::Written { etag: first } = store
        .put(
            "tokens/same-bytes",
            Bytes::from_static(b"same"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("the create succeeds")
    else {
        panic!("the create writes");
    };
    let head = store
        .head("tokens/same-bytes")
        .await
        .expect("the head succeeds")
        .expect("the object exists");
    let read = store
        .get("tokens/same-bytes")
        .await
        .expect("the read succeeds")
        .expect("the object exists");
    let listed = store
        .list("tokens/", None, 10)
        .await
        .expect("the listing succeeds");
    assert_eq!(head.etag, first, "head token");
    assert_eq!(read.etag, first, "get token");
    assert_eq!(listed.objects[0].etag, first, "list token");
    let exact = store
        .list("tokens/same-bytes", None, 10)
        .await
        .expect("the exact-prefix listing succeeds");
    assert_eq!(
        exact
            .objects
            .iter()
            .map(|object| object.key.as_str())
            .collect::<Vec<_>>(),
        ["tokens/same-bytes"],
        "a prefix equal to a key lists that key"
    );
    let WriteOutcome::Written { etag: second } = store
        .put(
            "tokens/same-bytes",
            Bytes::from_static(b"same"),
            WriteCondition::ReplaceIfEtagMatches {
                etag: first.clone(),
            },
        )
        .await
        .expect("the replacement succeeds")
    else {
        panic!("the replacement writes");
    };
    assert_ne!(second, first, "rewriting identical bytes changes the token");
    assert_eq!(
        store
            .put(
                "tokens/same-bytes",
                Bytes::from_static(b"same"),
                WriteCondition::ReplaceIfEtagMatches { etag: first },
            )
            .await
            .expect("a stale replacement is an outcome"),
        WriteOutcome::PreconditionFailed
    );
}

async fn check_single_key_pages_walk_every_key(store: &AzureGcsObjectStore) {
    let keys: Vec<String> = (0..5).map(|index| format!("pages/key-{index}")).collect();
    for key in &keys {
        store
            .put(key, Bytes::from_static(b"page"), WriteCondition::CreateOnly)
            .await
            .expect("the page key is written");
    }
    let mut walked = Vec::new();
    let mut start_after: Option<String> = None;
    loop {
        let listing = store
            .list("pages/", start_after.as_deref(), 1)
            .await
            .expect("the single-key page lists");
        walked.extend(listing.objects.into_iter().map(|object| object.key));
        match listing.next_start_after {
            Some(next) => start_after = Some(next),
            None => break,
        }
    }
    assert_eq!(
        walked, keys,
        "single-key pages return every key once, in order"
    );
}

async fn check_staged_create_of_a_large_file(store: &AzureGcsObjectStore) {
    let directory = tempfile::tempdir().expect("a temporary directory");
    let input = directory.path().join("large-input");
    let content: Vec<u8> = (0..STAGED_FILE_BYTES)
        .map(|index| u8::try_from(index % 251).expect("the value fits a byte"))
        .collect();
    tokio::fs::write(&input, &content)
        .await
        .expect("the input file is written");
    let WriteOutcome::Written { etag } = store
        .put_file("large/staged", &input, WriteCondition::CreateOnly)
        .await
        .expect("the staged create succeeds")
    else {
        panic!("the staged create writes");
    };
    assert_eq!(
        store
            .put_file("large/staged", &input, WriteCondition::CreateOnly)
            .await
            .expect("a second staged create is an outcome"),
        WriteOutcome::PreconditionFailed
    );
    let head = store
        .head("large/staged")
        .await
        .expect("the head succeeds")
        .expect("the object exists");
    assert_eq!(head.etag, etag);
    assert_eq!(head.size, STAGED_FILE_BYTES as u64);
    let output = directory.path().join("large-output");
    assert_eq!(
        store
            .get_to_file("large/staged", &output)
            .await
            .expect("the download succeeds"),
        Some(STAGED_FILE_BYTES as u64)
    );
    assert!(
        tokio::fs::read(&output)
            .await
            .expect("the download is readable")
            == content,
        "the staged object holds the uploaded bytes"
    );
    let staging = store
        .list(STAGING_KEY_PREFIX, None, 10)
        .await
        .expect("the staging listing succeeds");
    assert!(
        staging.objects.is_empty(),
        "staging objects remain: {:?}",
        staging.objects
    );
}

async fn delete_everything(store: &AzureGcsObjectStore) {
    loop {
        let listing = store
            .list("", None, 1000)
            .await
            .expect("the cleanup listing succeeds");
        if listing.objects.is_empty() {
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
            .expect("the cleanup deletes succeed");
    }
}
