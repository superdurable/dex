// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Where a soak keeps its objects: a real bucket that the environment names, or an s3local
//! emulator in this process.

use std::collections::BTreeMap;
use std::time::Duration;

use dexzd_object_store::{
    ObjectMetadata, ObjectStore, S3ApiDialect, S3Credentials, S3ObjectStore, S3ObjectStoreConfig,
};
use s3local_server::SignatureVerification;

use crate::harness::{ACCESS_KEY_ID, S3Emulator, SECRET_ACCESS_KEY, TestObjectStoreProvider};

const TARGET_VARIABLE: &str = "DEX_SOAK_TARGET";
const VERSION_DISCOVERY_VARIABLE: &str = "DEX_SOAK_VERSION_DISCOVERY";
const MANUAL_TEST_DOC: &str = "crates/dexzd-server/README.md";
const EMULATOR_BUCKET: &str = "dex-soak";
/// Repeats each listing for two seconds, within the one to three seconds measured on COS.
const STALE_LISTING_RULES: &str =
    r#"[{"operation":"ListObjectsV2","action":{"type":"stale_listing","ttl_milliseconds":2000}}]"#;
const LISTING_PAGE_KEYS: u32 = 1000;
/// Rereads of a page that adds no key, which outlast a COS listing that repeats for seconds.
const STALE_PAGE_RETRIES: usize = 5;
const STALE_PAGE_PAUSE: Duration = Duration::from_secs(1);
/// Rounds of listing and deleting before the cleanup reports what is left.
const CLEANUP_ROUNDS: usize = 5;
/// Listings this far apart that agree are past a COS listing cache.
const LISTING_CACHE_WINDOW: Duration = Duration::from_secs(5);
const SETTLED_LISTING_ATTEMPTS: usize = 6;

/// The provider of the soak's object storage.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum SoakProvider {
    TencentCos,
    AliyunOss { is_head_probing: bool },
}

impl SoakProvider {
    fn yaml_name(self) -> &'static str {
        match self {
            Self::TencentCos => "tencentCOS",
            Self::AliyunOss { .. } => "aliyunOSS",
        }
    }

    fn client_dialect(self) -> S3ApiDialect {
        match self {
            Self::TencentCos => S3ApiDialect::TencentCos,
            Self::AliyunOss { .. } => S3ApiDialect::AliyunOss,
        }
    }

    /// Returns whether the servers find versions with HEAD probing.
    pub(crate) fn is_head_probing(self) -> bool {
        match self {
            Self::TencentCos => true,
            Self::AliyunOss { is_head_probing } => is_head_probing,
        }
    }
}

impl std::fmt::Display for SoakProvider {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::TencentCos => write!(formatter, "tencentCOS"),
            Self::AliyunOss { is_head_probing } => write!(
                formatter,
                "aliyunOSS with versionDiscovery: {}",
                if *is_head_probing { "head" } else { "list" }
            ),
        }
    }
}

/// The bucket and prefix of one soak run, and the emulator that serves it when there is one.
pub(crate) struct SoakTarget {
    pub(crate) provider: SoakProvider,
    endpoint: String,
    region: String,
    bucket: String,
    prefix: String,
    access_key_id: String,
    secret_access_key: String,
    emulator: Option<S3Emulator>,
}

impl SoakTarget {
    /// Reads the target from the environment; an `s3local-` target starts an emulator.
    ///
    /// Panics naming every missing variable.
    pub(crate) async fn from_environment(run_id: &str) -> Self {
        let target = std::env::var(TARGET_VARIABLE).unwrap_or_default();
        let is_head_probing = match std::env::var(VERSION_DISCOVERY_VARIABLE)
            .unwrap_or_default()
            .as_str()
        {
            "" | "list" => false,
            "head" => true,
            other => panic!("{VERSION_DISCOVERY_VARIABLE} must be list or head, not {other:?}"),
        };
        let prefix = format!("dexzd-soak/{run_id}");
        match target.as_str() {
            "tencentCOS" => Self::real(SoakProvider::TencentCos, "DEX_COS", prefix),
            "aliyunOSS" => Self::real(
                SoakProvider::AliyunOss { is_head_probing },
                "DEX_OSS",
                prefix,
            ),
            "s3local-tencentCOS" => Self::emulated(SoakProvider::TencentCos, prefix).await,
            "s3local-aliyunOSS" => {
                Self::emulated(SoakProvider::AliyunOss { is_head_probing }, prefix).await
            }
            other => panic!(
                "set {TARGET_VARIABLE} to tencentCOS, aliyunOSS, s3local-tencentCOS, or \
                 s3local-aliyunOSS, not {other:?}; see {MANUAL_TEST_DOC}"
            ),
        }
    }

    fn real(provider: SoakProvider, variable_prefix: &str, prefix: String) -> Self {
        let names = [
            "ENDPOINT",
            "REGION",
            "BUCKET",
            "ACCESS_KEY_ID",
            "SECRET_ACCESS_KEY",
        ]
        .map(|name| format!("{variable_prefix}_{name}"));
        let values: Vec<Option<String>> = names
            .iter()
            .map(|name| std::env::var(name).ok().filter(|value| !value.is_empty()))
            .collect();
        let missing: Vec<&str> = names
            .iter()
            .zip(&values)
            .filter(|(_, value)| value.is_none())
            .map(|(name, _)| name.as_str())
            .collect();
        assert!(
            missing.is_empty(),
            "set {} to run the soak on a real bucket; see {MANUAL_TEST_DOC}",
            missing.join(", ")
        );
        let mut values = values
            .into_iter()
            .map(|value| value.expect("checked above"));
        let mut next = || values.next().expect("one value per name");
        Self {
            provider,
            endpoint: next(),
            region: next(),
            bucket: next(),
            access_key_id: next(),
            secret_access_key: next(),
            prefix,
            emulator: None,
        }
    }

    async fn emulated(provider: SoakProvider, prefix: String) -> Self {
        let test_provider = match provider {
            SoakProvider::TencentCos => TestObjectStoreProvider::TencentCos,
            SoakProvider::AliyunOss { .. } => TestObjectStoreProvider::AliyunOss,
        };
        let emulator = S3Emulator::start_with_dialect(
            EMULATOR_BUCKET,
            SignatureVerification::Strict,
            test_provider.api_dialect(),
        )
        .await;
        if provider == SoakProvider::TencentCos {
            emulator.set_fault_rules(STALE_LISTING_RULES).await;
        }
        Self {
            provider,
            endpoint: emulator.endpoint(),
            region: "us-east-1".to_owned(),
            bucket: EMULATOR_BUCKET.to_owned(),
            prefix,
            access_key_id: ACCESS_KEY_ID.to_owned(),
            secret_access_key: SECRET_ACCESS_KEY.to_owned(),
            emulator: Some(emulator),
        }
    }

    /// Returns a one-line description that names no secret.
    pub(crate) fn describe(&self) -> String {
        let place = if self.emulator.is_some() {
            "s3local emulator"
        } else {
            "real bucket"
        };
        format!(
            "{} on {place} {}/{} at {}",
            self.provider, self.bucket, self.prefix, self.endpoint
        )
    }

    pub(crate) fn is_emulated(&self) -> bool {
        self.emulator.is_some()
    }

    /// Holds every log batch write on the emulator for `hold`, keeping its other rules.
    pub(crate) async fn start_log_stall(&self, hold: Duration) {
        let emulator = self
            .emulator
            .as_ref()
            .expect("only an emulated target stalls log writes");
        // COS writes create-only objects with AppendObject.
        let stall = ["PutObject", "AppendObject"]
            .map(|operation| {
                format!(
                    r#"{{"operation":"{operation}","key_prefix":"{}/log/","action":{{"type":"delay","milliseconds":{}}}}}"#,
                    self.prefix,
                    hold.as_millis()
                )
            })
            .join(",");
        let rules = match self.provider {
            SoakProvider::TencentCos => format!(
                "[{},{stall}]",
                STALE_LISTING_RULES
                    .trim_start_matches('[')
                    .trim_end_matches(']')
            ),
            SoakProvider::AliyunOss { .. } => format!("[{stall}]"),
        };
        emulator.set_fault_rules(&rules).await;
    }

    /// Releases the writes that a log stall holds and restores the emulator's other rules.
    pub(crate) async fn end_log_stall(&self) {
        let emulator = self
            .emulator
            .as_ref()
            .expect("only an emulated target stalls log writes");
        emulator.clear_fault_rules().await;
        if self.provider == SoakProvider::TencentCos {
            emulator.set_fault_rules(STALE_LISTING_RULES).await;
        }
    }

    pub(crate) fn credentials(&self) -> (&str, &str) {
        (&self.access_key_id, &self.secret_access_key)
    }

    /// Returns the `objectStore` section of a serve configuration.
    pub(crate) fn object_store_yaml(&self) -> String {
        let version_discovery = match self.provider {
            SoakProvider::TencentCos => String::new(),
            SoakProvider::AliyunOss { is_head_probing } => format!(
                "  versionDiscovery: {}\n",
                if is_head_probing { "head" } else { "list" }
            ),
        };
        format!(
            "objectStore:\n  provider: {provider}\n{version_discovery}  endpoint: {endpoint}\n  region: {region}\n  bucket: {bucket}\n  prefix: {prefix}\n  credentialsSource: environment\n",
            provider = self.provider.yaml_name(),
            endpoint = self.endpoint,
            region = self.region,
            bucket = self.bucket,
            prefix = self.prefix,
        )
    }

    /// Returns a store of the physical objects under the soak prefix.
    pub(crate) async fn physical_store(&self) -> S3ObjectStore {
        let mut config = S3ObjectStoreConfig::new(self.region.clone(), self.bucket.clone());
        config.endpoint = Some(self.endpoint.clone());
        config.key_prefix = self.prefix.clone();
        config.force_path_style = self.emulator.is_some();
        config.credentials = S3Credentials::Static {
            access_key_id: self.access_key_id.clone(),
            secret_access_key: self.secret_access_key.clone(),
            session_token: None,
        };
        config.api_dialect = self.provider.client_dialect();
        S3ObjectStore::new(config)
            .await
            .expect("the soak's inspection store is configured")
    }

    /// Deletes every object under the prefix and returns the keys that a settled listing still
    /// shows afterwards.
    ///
    /// # Errors
    ///
    /// Returns a description of a failed listing, or of listings that never settled.
    pub(crate) async fn delete_everything(&self) -> Result<Vec<String>, String> {
        let store = self.physical_store().await;
        for _ in 0..CLEANUP_ROUNDS {
            let keys = settled_keys(&store).await?;
            if keys.is_empty() {
                return Ok(keys);
            }
            for batch in keys.chunks(LISTING_PAGE_KEYS as usize) {
                if let Err(error) = store.delete_many(batch).await {
                    eprintln!(
                        "soak cleanup: deleting {} objects failed: {error}",
                        batch.len()
                    );
                }
            }
            // A listing may repeat deleted keys for seconds, so the next round lists again.
        }
        settled_keys(&store).await
    }

    /// Returns every key under the prefix, from a settled listing.
    ///
    /// # Errors
    ///
    /// Returns a description of a failed listing, or of listings that never settled.
    pub(crate) async fn remaining_keys(&self) -> Result<Vec<String>, String> {
        settled_keys(&self.physical_store().await).await
    }

    pub(crate) async fn shutdown(self) {
        if let Some(emulator) = self.emulator {
            emulator.shutdown().await;
        }
    }
}

/// Lists every object under `prefix` once.
///
/// A COS page may repeat and ignore `start_after`; a page without a new key is read again.
///
/// # Errors
///
/// Returns the store's error when a page fails.
pub(crate) async fn list_all(
    store: &S3ObjectStore,
    prefix: &str,
) -> Result<Vec<ObjectMetadata>, dexzd_object_store::ObjectStoreError> {
    let mut objects: BTreeMap<String, ObjectMetadata> = BTreeMap::new();
    let mut start_after: Option<String> = None;
    let mut stale_pages = 0;
    loop {
        let listing = store
            .list(prefix, start_after.as_deref(), LISTING_PAGE_KEYS)
            .await?;
        let is_truncated = listing.next_start_after.is_some();
        let mut last_key = None;
        for object in listing.objects {
            if start_after
                .as_ref()
                .is_some_and(|start| object.key <= *start)
            {
                continue;
            }
            last_key = Some(object.key.clone());
            objects.insert(object.key.clone(), object);
        }
        match last_key {
            Some(last_key) if is_truncated => {
                start_after = Some(last_key);
                stale_pages = 0;
            }
            Some(_) => return Ok(objects.into_values().collect()),
            None if start_after.is_none() || stale_pages >= STALE_PAGE_RETRIES => {
                return Ok(objects.into_values().collect());
            }
            None => {
                stale_pages += 1;
                tokio::time::sleep(STALE_PAGE_PAUSE).await;
            }
        }
    }
}

/// Returns every key under the prefix once two listings a cache window apart agree.
///
/// # Errors
///
/// Returns a description of a failed listing, or of listings that never agreed.
async fn settled_keys(store: &S3ObjectStore) -> Result<Vec<String>, String> {
    let mut previous = listed_keys(store).await?;
    for _ in 0..SETTLED_LISTING_ATTEMPTS {
        tokio::time::sleep(LISTING_CACHE_WINDOW).await;
        let current = listed_keys(store).await?;
        if current == previous {
            return Ok(current);
        }
        previous = current;
    }
    Err(format!(
        "{SETTLED_LISTING_ATTEMPTS} listings of the prefix {LISTING_CACHE_WINDOW:?} apart never agreed"
    ))
}

async fn listed_keys(store: &S3ObjectStore) -> Result<Vec<String>, String> {
    list_all(store, "")
        .await
        .map(|objects| objects.into_iter().map(|object| object.key).collect())
        .map_err(|error| format!("listing the prefix failed: {error}"))
}

/// Counts objects and bytes by their first path segment, or by the key before `.v/`.
pub(crate) fn count_by_area(objects: &[ObjectMetadata]) -> BTreeMap<String, (usize, u64)> {
    let mut counts: BTreeMap<String, (usize, u64)> = BTreeMap::new();
    for object in objects {
        let area = match object.key.split_once('/') {
            Some((first, _)) => format!("{first}/"),
            None => object.key.clone(),
        };
        let entry = counts.entry(area).or_default();
        entry.0 += 1;
        entry.1 += object.size;
    }
    counts
}
