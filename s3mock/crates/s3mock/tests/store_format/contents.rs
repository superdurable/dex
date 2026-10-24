// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Logical store contents read through the public API. Explicit fields, not record
//! serialization, let additive record fields still match.

use std::collections::BTreeMap;
use std::io::Read;

use s3mock::{
    BucketRecord, ContentHeaders, CorsRule, CredentialStatus, LifecycleRule,
    ListMultipartUploadsRequest, ListObjectVersionsRequest, ListObjectsRequest, ListPartsRequest,
    ObjectChecksum, ObjectReadRequest, PartRecord, Store, UploadRecord, VersionRecord,
    VersioningState,
};
use serde::{Deserialize, Serialize};

/// Large enough that every recorded listing fits in one page.
const PAGE_SIZE: usize = 1000;

/// Everything the expectations file records about a store.
#[derive(Debug, PartialEq, Eq, Serialize, Deserialize)]
pub(crate) struct StoreContents {
    pub(crate) buckets: Vec<BucketContents>,
    pub(crate) credentials: Vec<CredentialContents>,
}

#[derive(Debug, PartialEq, Eq, Serialize, Deserialize)]
pub(crate) struct BucketContents {
    pub(crate) name: String,
    pub(crate) incarnation: String,
    pub(crate) created_unix_ms: i64,
    pub(crate) versioning: String,
    pub(crate) object_lock: Option<ObjectLockContents>,
    pub(crate) lifecycle_rules: Option<Vec<LifecycleRuleContents>>,
    pub(crate) cors_rules: Option<Vec<CorsRuleContents>>,
    /// Every version and delete marker from ListObjectVersions.
    pub(crate) versions: Vec<VersionContents>,
    /// The current objects from ListObjectsV2.
    pub(crate) current_objects: Vec<CurrentObjectContents>,
    pub(crate) uploads: Vec<UploadContents>,
}

#[derive(Debug, PartialEq, Eq, Serialize, Deserialize)]
pub(crate) struct ObjectLockContents {
    pub(crate) default_retention_mode: Option<String>,
    pub(crate) default_retention_days: Option<u32>,
    pub(crate) default_retention_years: Option<u32>,
}

#[derive(Debug, PartialEq, Eq, Serialize, Deserialize)]
pub(crate) struct LifecycleRuleContents {
    pub(crate) id: Option<String>,
    pub(crate) is_enabled: bool,
    pub(crate) prefix: String,
    pub(crate) expiration_days: Option<u32>,
    pub(crate) expired_object_delete_marker: Option<bool>,
    pub(crate) noncurrent_expiration_days: Option<u32>,
    pub(crate) newer_noncurrent_versions: Option<u32>,
    pub(crate) abort_incomplete_upload_days: Option<u32>,
}

#[derive(Debug, PartialEq, Eq, Serialize, Deserialize)]
pub(crate) struct CorsRuleContents {
    pub(crate) id: Option<String>,
    pub(crate) allowed_origins: Vec<String>,
    pub(crate) allowed_methods: Vec<String>,
    pub(crate) allowed_headers: Vec<String>,
    pub(crate) expose_headers: Vec<String>,
    pub(crate) max_age_seconds: Option<u32>,
}

#[derive(Debug, PartialEq, Eq, Serialize, Deserialize)]
pub(crate) struct VersionContents {
    pub(crate) key: String,
    pub(crate) version_id: String,
    pub(crate) internal_version: String,
    pub(crate) is_latest: bool,
    pub(crate) is_null_version: bool,
    pub(crate) is_delete_marker: bool,
    pub(crate) size: u64,
    pub(crate) etag: String,
    pub(crate) checksum: Option<ChecksumContents>,
    pub(crate) content_headers: ContentHeadersContents,
    pub(crate) user_metadata: BTreeMap<String, String>,
    pub(crate) last_modified_unix_ms: i64,
    pub(crate) data_file: Option<String>,
    pub(crate) parts_count: Option<u32>,
    /// The body read back by version ID; absent for delete markers.
    pub(crate) body: Option<String>,
}

#[derive(Debug, PartialEq, Eq, Serialize, Deserialize)]
pub(crate) struct CurrentObjectContents {
    pub(crate) key: String,
    pub(crate) version_id: String,
}

#[derive(Debug, PartialEq, Eq, Serialize, Deserialize)]
pub(crate) struct ChecksumContents {
    pub(crate) algorithm: String,
    pub(crate) base64_value: String,
    pub(crate) checksum_type: String,
}

#[derive(Debug, PartialEq, Eq, Serialize, Deserialize)]
pub(crate) struct ContentHeadersContents {
    pub(crate) content_type: Option<String>,
    pub(crate) content_encoding: Option<String>,
    pub(crate) content_disposition: Option<String>,
    pub(crate) content_language: Option<String>,
    pub(crate) cache_control: Option<String>,
    pub(crate) expires: Option<String>,
}

#[derive(Debug, PartialEq, Eq, Serialize, Deserialize)]
pub(crate) struct UploadContents {
    pub(crate) upload_id: String,
    pub(crate) key: String,
    pub(crate) bucket_incarnation: String,
    pub(crate) initiated_unix_ms: i64,
    pub(crate) content_headers: ContentHeadersContents,
    pub(crate) user_metadata: BTreeMap<String, String>,
    pub(crate) checksum_algorithm: Option<String>,
    pub(crate) checksum_type: String,
    pub(crate) parts: Vec<PartContents>,
}

#[derive(Debug, PartialEq, Eq, Serialize, Deserialize)]
pub(crate) struct PartContents {
    pub(crate) part_number: u32,
    pub(crate) size: u64,
    pub(crate) etag: String,
    pub(crate) checksum: ChecksumContents,
    pub(crate) last_modified_unix_ms: i64,
    pub(crate) data_file: String,
}

#[derive(Debug, PartialEq, Eq, Serialize, Deserialize)]
pub(crate) struct CredentialContents {
    pub(crate) access_key_id: String,
    pub(crate) secret_access_key: String,
    pub(crate) parent: String,
    pub(crate) description: String,
    pub(crate) policy: serde_json::Value,
    pub(crate) status: String,
}

/// Reads every bucket, version, upload, and the credentials owned by `credential_parent`.
pub(crate) fn read_store_contents(store: &Store, credential_parent: &str) -> StoreContents {
    let buckets = store
        .list_buckets()
        .expect("the buckets table is readable")
        .iter()
        .map(|bucket| read_bucket_contents(store, bucket))
        .collect();
    let credentials = store
        .credentials_by_parent(credential_parent)
        .expect("the credentials table is readable")
        .into_iter()
        .map(|credential| CredentialContents {
            access_key_id: credential.access_key_id,
            secret_access_key: credential.secret_access_key,
            parent: credential.parent,
            description: credential.description,
            policy: credential.policy,
            status: match credential.status {
                CredentialStatus::Enabled => "enabled".to_owned(),
                CredentialStatus::Disabled => "disabled".to_owned(),
            },
        })
        .collect();
    StoreContents {
        buckets,
        credentials,
    }
}

fn read_bucket_contents(store: &Store, bucket: &BucketRecord) -> BucketContents {
    BucketContents {
        name: bucket.name.clone(),
        incarnation: bucket.incarnation.clone(),
        created_unix_ms: bucket.created_unix_ms,
        versioning: versioning_name(bucket.versioning).to_owned(),
        object_lock: bucket.object_lock.as_ref().map(|configuration| {
            let retention = configuration.default_retention.as_ref();
            ObjectLockContents {
                default_retention_mode: retention
                    .map(|retention| retention.mode.s3_name().to_owned()),
                default_retention_days: retention.and_then(|retention| retention.days),
                default_retention_years: retention.and_then(|retention| retention.years),
            }
        }),
        lifecycle_rules: bucket.lifecycle.as_ref().map(|configuration| {
            configuration
                .rules
                .iter()
                .map(lifecycle_rule_contents)
                .collect()
        }),
        cors_rules: bucket
            .cors
            .as_ref()
            .map(|configuration| configuration.rules.iter().map(cors_rule_contents).collect()),
        versions: read_versions(store, &bucket.name),
        current_objects: read_current_objects(store, &bucket.name),
        uploads: read_uploads(store, &bucket.name),
    }
}

fn read_versions(store: &Store, bucket: &str) -> Vec<VersionContents> {
    let page = store
        .list_object_versions(
            bucket,
            &ListObjectVersionsRequest {
                prefix: String::new(),
                delimiter: String::new(),
                max_keys: PAGE_SIZE,
                key_marker: None,
                version_id_marker: None,
            },
        )
        .expect("the bucket's versions are listable");
    assert!(
        !page.is_truncated,
        "every recorded version fits in one page"
    );
    page.versions
        .into_iter()
        .map(|listed| {
            let body = (!listed.record.is_delete_marker)
                .then(|| read_body(store, bucket, &listed.key, &listed.record.version_id()));
            version_contents(listed.key, listed.is_latest, &listed.record, body)
        })
        .collect()
}

fn read_body(store: &Store, bucket: &str, key: &str, version_id: &str) -> String {
    let object = store
        .read_object(
            bucket,
            key,
            &ObjectReadRequest {
                version_id: Some(version_id.to_owned()),
                ..ObjectReadRequest::default()
            },
        )
        .expect("every recorded version has a readable data file");
    let mut body = String::new();
    object
        .file
        .take(object.length)
        .read_to_string(&mut body)
        .expect("recorded bodies are UTF-8 text");
    body
}

fn read_current_objects(store: &Store, bucket: &str) -> Vec<CurrentObjectContents> {
    let page = store
        .list_objects(
            bucket,
            &ListObjectsRequest {
                prefix: String::new(),
                delimiter: String::new(),
                max_keys: PAGE_SIZE,
                continuation_token: None,
                start_after: None,
            },
        )
        .expect("the bucket's objects are listable");
    assert!(!page.is_truncated, "every current object fits in one page");
    page.objects
        .into_iter()
        .map(|listed| CurrentObjectContents {
            version_id: listed.record.version_id(),
            key: listed.key,
        })
        .collect()
}

fn read_uploads(store: &Store, bucket: &str) -> Vec<UploadContents> {
    let page = store
        .list_multipart_uploads(
            bucket,
            &ListMultipartUploadsRequest {
                prefix: String::new(),
                delimiter: String::new(),
                key_marker: None,
                upload_id_marker: None,
                max_uploads: PAGE_SIZE,
            },
        )
        .expect("the bucket's uploads are listable");
    assert!(!page.is_truncated, "every upload fits in one page");
    page.uploads
        .iter()
        .map(|upload| {
            let parts = store
                .list_parts(
                    bucket,
                    &upload.key,
                    &upload.upload_id,
                    &ListPartsRequest {
                        part_number_marker: 0,
                        max_parts: PAGE_SIZE,
                    },
                )
                .expect("the upload's parts are listable");
            assert!(!parts.is_truncated, "every part fits in one page");
            upload_contents(upload, &parts.parts)
        })
        .collect()
}

fn version_contents(
    key: String,
    is_latest: bool,
    record: &VersionRecord,
    body: Option<String>,
) -> VersionContents {
    VersionContents {
        key,
        version_id: record.version_id(),
        internal_version: record.internal_version.clone(),
        is_latest,
        is_null_version: record.is_null_version,
        is_delete_marker: record.is_delete_marker,
        size: record.size,
        etag: record.etag.clone(),
        checksum: record.checksum.as_ref().map(checksum_contents),
        content_headers: content_headers_contents(&record.content_headers),
        user_metadata: record.user_metadata.clone(),
        last_modified_unix_ms: record.last_modified_unix_ms,
        data_file: record.data_file.clone(),
        parts_count: record.parts_count,
        body,
    }
}

fn upload_contents(upload: &UploadRecord, parts: &[PartRecord]) -> UploadContents {
    UploadContents {
        upload_id: upload.upload_id.clone(),
        key: upload.key.clone(),
        bucket_incarnation: upload.bucket_incarnation.clone(),
        initiated_unix_ms: upload.initiated_unix_ms,
        content_headers: content_headers_contents(&upload.content_headers),
        user_metadata: upload.user_metadata.clone(),
        checksum_algorithm: upload
            .checksum_algorithm
            .map(|algorithm| algorithm.s3_name().to_owned()),
        checksum_type: upload.checksum_type.s3_name().to_owned(),
        parts: parts
            .iter()
            .map(|part| PartContents {
                part_number: part.part_number,
                size: part.size,
                etag: part.etag.clone(),
                checksum: checksum_contents(&part.checksum),
                last_modified_unix_ms: part.last_modified_unix_ms,
                data_file: part.data_file.clone(),
            })
            .collect(),
    }
}

fn lifecycle_rule_contents(rule: &LifecycleRule) -> LifecycleRuleContents {
    LifecycleRuleContents {
        id: rule.id.clone(),
        is_enabled: rule.is_enabled,
        prefix: rule.prefix.clone(),
        expiration_days: rule.expiration_days,
        expired_object_delete_marker: rule.expired_object_delete_marker,
        noncurrent_expiration_days: rule.noncurrent_expiration_days,
        newer_noncurrent_versions: rule.newer_noncurrent_versions,
        abort_incomplete_upload_days: rule.abort_incomplete_upload_days,
    }
}

fn cors_rule_contents(rule: &CorsRule) -> CorsRuleContents {
    CorsRuleContents {
        id: rule.id.clone(),
        allowed_origins: rule.allowed_origins.clone(),
        allowed_methods: rule.allowed_methods.clone(),
        allowed_headers: rule.allowed_headers.clone(),
        expose_headers: rule.expose_headers.clone(),
        max_age_seconds: rule.max_age_seconds,
    }
}

fn checksum_contents(checksum: &ObjectChecksum) -> ChecksumContents {
    ChecksumContents {
        algorithm: checksum.algorithm.s3_name().to_owned(),
        base64_value: checksum.base64_value.clone(),
        checksum_type: checksum.checksum_type.s3_name().to_owned(),
    }
}

fn content_headers_contents(headers: &ContentHeaders) -> ContentHeadersContents {
    ContentHeadersContents {
        content_type: headers.content_type.clone(),
        content_encoding: headers.content_encoding.clone(),
        content_disposition: headers.content_disposition.clone(),
        content_language: headers.content_language.clone(),
        cache_control: headers.cache_control.clone(),
        expires: headers.expires.clone(),
    }
}

fn versioning_name(versioning: VersioningState) -> &'static str {
    match versioning {
        VersioningState::Unversioned => "Unversioned",
        VersioningState::Enabled => "Enabled",
        VersioningState::Suspended => "Suspended",
    }
}
