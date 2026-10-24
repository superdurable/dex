// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Recorded stores under `tests/testdata/store_format_v<N>` open and read back unchanged.

use std::collections::BTreeSet;
use std::fs;
use std::io::Read;
use std::path::{Path, PathBuf};

use s3mock::{
    CompleteMultipartUploadRequest, CompletedPart, Durability, ListMultipartUploadsRequest,
    ListPartsRequest, ObjectReadRequest, Store, StoreError,
};
use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};

use crate::contents::{StoreContents, VersionContents, read_store_contents};
use crate::store_files::{
    copy_directory, open_store, read_store_format_version, remove_store_format_table,
};
use crate::workload::{
    CREDENTIAL_PARENT, HISTORY_KEY, OBJECT_LOCK_BUCKET, PENDING_CHECKSUMMED_UPLOAD_BODY,
    PENDING_CHECKSUMMED_UPLOAD_KEY, PENDING_PLAIN_UPLOAD_KEY, SUSPENDED_BUCKET, UNVERSIONED_BUCKET,
    UploadAddress, VERSIONED_BUCKET,
};

pub(crate) const MANIFEST_FILE_NAME: &str = "manifest.json";
pub(crate) const EXPECTED_CONTENTS_FILE_NAME: &str = "expected_contents.json";
pub(crate) const STORE_DIRECTORY_NAME: &str = "store";
/// A valid upload ID that no recorded store contains.
const UNKNOWN_UPLOAD_ID: &str = "01JAAAAAAAAAAAAAAAAAAAAAAA";

/// Describes who wrote a recorded store.
#[derive(Debug, Serialize, Deserialize)]
pub(crate) struct RecordedManifest {
    pub(crate) store_format_version: u64,
    /// The s3mock workspace version that ran the generator.
    pub(crate) s3mock_version: String,
}

/// What reading a recorded store must return.
#[derive(Debug, Serialize, Deserialize)]
pub(crate) struct RecordedContents {
    pub(crate) store: StoreContents,
    /// Aborted uploads disappear from listings, so their IDs are recorded here.
    pub(crate) aborted_uploads: Vec<UploadAddress>,
}

#[test]
fn recorded_store_format_v1_reads_back() {
    let recorded = recorded_directory(1);
    let directory = tempfile::tempdir().expect("a temporary directory is created");
    copy_directory(&recorded.join(STORE_DIRECTORY_NAME), directory.path());
    assert_eq!(read_store_format_version(directory.path()), Some(1));
    assert_recorded_store_reads_back(&recorded, directory.path());
}

/// Stores written before the format version existed hold the same records without the marker.
#[test]
fn recorded_store_format_v1_without_format_version_reads_back() {
    let recorded = recorded_directory(1);
    let directory = tempfile::tempdir().expect("a temporary directory is created");
    copy_directory(&recorded.join(STORE_DIRECTORY_NAME), directory.path());
    remove_store_format_table(directory.path());
    assert_recorded_store_reads_back(&recorded, directory.path());
}

fn recorded_directory(store_format_version: u64) -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("tests")
        .join("testdata")
        .join(format!("store_format_v{store_format_version}"))
}

/// Opens a copy of a recorded store, compares it with its expectations, then exercises its files.
fn assert_recorded_store_reads_back(recorded: &Path, root: &Path) {
    let manifest: RecordedManifest = read_json(&recorded.join(MANIFEST_FILE_NAME));
    assert_eq!(manifest.store_format_version, 1);
    assert_eq!(manifest.s3mock_version, "0.1.0");
    let expected: RecordedContents = read_json(&recorded.join(EXPECTED_CONTENTS_FILE_NAME));
    assert_workload_shape(&expected.store);

    let store = open_store(root, Durability::Strict);
    assert_eq!(
        read_store_contents(&store, CREDENTIAL_PARENT),
        expected.store
    );
    for bucket in &expected.store.buckets {
        for current in &bucket.current_objects {
            let selection = store
                .head_object(&bucket.name, &current.key, &ObjectReadRequest::default())
                .expect("every current object resolves without a version ID");
            assert_eq!(selection.record.version_id(), current.version_id);
        }
    }
    assert_aborted_uploads_stay_aborted(&store, &expected.aborted_uploads);
    complete_pending_checksummed_upload(&store);
    let collection = store
        .collect_orphan_data_files()
        .expect("orphan collection reads every bucket");
    assert!(
        collection.examined_files > 0,
        "recorded data files are examined"
    );
    assert_eq!(
        collection.removed_files, 0,
        "every recorded data file is referenced"
    );
    drop(store);
    assert_eq!(read_store_format_version(root), Some(1));
}

/// A repeated abort succeeds only through the recorded aborted-upload entry.
fn assert_aborted_uploads_stay_aborted(store: &Store, aborted_uploads: &[UploadAddress]) {
    assert!(!aborted_uploads.is_empty(), "the workload aborts an upload");
    for aborted in aborted_uploads {
        store
            .abort_multipart_upload(&aborted.bucket, &aborted.key, &aborted.upload_id)
            .expect("a recorded aborted upload can be aborted again");
        match store.abort_multipart_upload(&aborted.bucket, &aborted.key, UNKNOWN_UPLOAD_ID) {
            Err(StoreError::NoSuchUpload { .. }) => {}
            other => panic!("an unknown upload must not abort: {other:?}"),
        }
    }
}

/// Completing the recorded upload proves its part data file is readable.
fn complete_pending_checksummed_upload(store: &Store) {
    let upload = store
        .list_multipart_uploads(
            UNVERSIONED_BUCKET,
            &ListMultipartUploadsRequest {
                prefix: PENDING_CHECKSUMMED_UPLOAD_KEY.to_owned(),
                delimiter: String::new(),
                key_marker: None,
                upload_id_marker: None,
                max_uploads: 1,
            },
        )
        .expect("the bucket's uploads are listable")
        .uploads
        .pop()
        .expect("the recorded checksummed upload is in progress");
    let parts = store
        .list_parts(
            UNVERSIONED_BUCKET,
            &upload.key,
            &upload.upload_id,
            &ListPartsRequest {
                part_number_marker: 0,
                max_parts: 1000,
            },
        )
        .expect("the upload's parts are listable")
        .parts;
    store
        .complete_multipart_upload(
            UNVERSIONED_BUCKET,
            &upload.key,
            &upload.upload_id,
            CompleteMultipartUploadRequest {
                parts: parts
                    .into_iter()
                    .map(|part| CompletedPart {
                        part_number: part.part_number,
                        etag: part.etag,
                        checksum_base64: Some(part.checksum.base64_value),
                    })
                    .collect(),
                ..CompleteMultipartUploadRequest::default()
            },
        )
        .expect("the recorded part completes the upload");
    let object = store
        .read_object(
            UNVERSIONED_BUCKET,
            PENDING_CHECKSUMMED_UPLOAD_KEY,
            &ObjectReadRequest::default(),
        )
        .expect("the completed object is readable");
    let mut body = String::new();
    object
        .file
        .take(object.length)
        .read_to_string(&mut body)
        .expect("the completed body is UTF-8 text");
    assert_eq!(body, PENDING_CHECKSUMMED_UPLOAD_BODY);
}

/// Checks the recorded workload independently of the expectations file, so an incomplete file fails.
pub(crate) fn assert_workload_shape(contents: &StoreContents) {
    let bucket_names: BTreeSet<&str> = contents
        .buckets
        .iter()
        .map(|bucket| bucket.name.as_str())
        .collect();
    assert_eq!(
        bucket_names,
        BTreeSet::from([
            UNVERSIONED_BUCKET,
            VERSIONED_BUCKET,
            SUSPENDED_BUCKET,
            OBJECT_LOCK_BUCKET
        ])
    );
    let bucket = |name: &str| {
        contents
            .buckets
            .iter()
            .find(|bucket| bucket.name == name)
            .expect("every workload bucket is present")
    };

    let unversioned = bucket(UNVERSIONED_BUCKET);
    assert_eq!(unversioned.versioning, "Unversioned");
    assert_eq!(unversioned.cors_rules.as_ref().map(Vec::len), Some(2));
    assert_eq!(unversioned.lifecycle_rules.as_ref().map(Vec::len), Some(1));
    assert!(
        unversioned
            .versions
            .iter()
            .all(|version| version.is_null_version && version.body.is_some())
    );
    assert!(unversioned.versions.iter().any(|version| {
        version.parts_count == Some(1)
            && version
                .checksum
                .as_ref()
                .map(|checksum| checksum.checksum_type.as_str())
                == Some("COMPOSITE")
    }));
    assert!(unversioned.versions.iter().any(|version| {
        version.content_headers.content_type.is_some()
            && version.content_headers.expires.is_some()
            && !version.user_metadata.is_empty()
    }));
    let upload_parts: Vec<(&str, usize)> = unversioned
        .uploads
        .iter()
        .map(|upload| (upload.key.as_str(), upload.parts.len()))
        .collect();
    assert_eq!(
        upload_parts,
        [
            (PENDING_PLAIN_UPLOAD_KEY, 2),
            (PENDING_CHECKSUMMED_UPLOAD_KEY, 1)
        ]
    );

    let versioned = bucket(VERSIONED_BUCKET);
    assert_eq!(versioned.versioning, "Enabled");
    assert_eq!(versioned.lifecycle_rules.as_ref().map(Vec::len), Some(2));
    let history: Vec<&VersionContents> = versioned
        .versions
        .iter()
        .filter(|version| version.key == HISTORY_KEY)
        .collect();
    assert_eq!(history.len(), 4, "three versions under a delete marker");
    assert!(history[0].is_latest && history[0].is_delete_marker);
    let history_bodies: Vec<Option<&str>> = history[1..]
        .iter()
        .map(|version| version.body.as_deref())
        .collect();
    assert_eq!(
        history_bodies,
        [Some("history 3"), Some("history 2"), Some("history 1")]
    );
    assert!(
        versioned
            .current_objects
            .iter()
            .all(|current| current.key != HISTORY_KEY)
    );

    let suspended = bucket(SUSPENDED_BUCKET);
    assert_eq!(suspended.versioning, "Suspended");
    let null_flags: Vec<bool> = suspended
        .versions
        .iter()
        .map(|version| version.is_null_version)
        .collect();
    assert_eq!(null_flags, [true, false]);

    let object_lock = bucket(OBJECT_LOCK_BUCKET)
        .object_lock
        .as_ref()
        .expect("the bucket has Object Lock");
    assert_eq!(
        object_lock.default_retention_mode.as_deref(),
        Some("GOVERNANCE")
    );

    let algorithms: BTreeSet<&str> = contents
        .buckets
        .iter()
        .flat_map(|bucket| {
            let version_algorithms = bucket
                .versions
                .iter()
                .filter_map(|version| version.checksum.as_ref());
            let part_algorithms = bucket
                .uploads
                .iter()
                .flat_map(|upload| upload.parts.iter().map(|part| &part.checksum));
            version_algorithms.chain(part_algorithms)
        })
        .map(|checksum| checksum.algorithm.as_str())
        .collect();
    assert_eq!(
        algorithms,
        BTreeSet::from(["CRC32", "CRC32C", "CRC64NVME", "SHA1", "SHA256"])
    );

    let statuses: Vec<&str> = contents
        .credentials
        .iter()
        .map(|credential| credential.status.as_str())
        .collect();
    assert_eq!(statuses, ["enabled", "disabled"]);
}

fn read_json<T: DeserializeOwned>(path: &Path) -> T {
    let bytes = fs::read(path).unwrap_or_else(|error| panic!("read {}: {error}", path.display()));
    serde_json::from_slice(&bytes)
        .unwrap_or_else(|error| panic!("parse {}: {error}", path.display()))
}

pub(crate) fn write_json<T: Serialize>(path: &Path, value: &T) {
    let mut json = serde_json::to_string_pretty(value).expect("recorded values serialize");
    json.push('\n');
    fs::write(path, json).unwrap_or_else(|error| panic!("write {}: {error}", path.display()));
}
