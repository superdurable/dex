// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Physical removal of startup probe keys, which only their creating process uses.

use std::time::Duration;

use bytes::Bytes;

use crate::aliyun_oss::version_keys::{version_prefix, versioned_logical_key};
use crate::aliyun_oss::version_reads::inner_error;
use crate::{
    ObjectMetadata, ObjectStore, ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation,
    ObjectStoreResult, STARTUP_PROBE_MAXIMUM_LIFETIME, STARTUP_PROBE_OBJECT_KEY_PREFIX,
    WriteCondition, WriteOutcome,
};

/// Age, by store time, after which
/// [`crate::AliyunOssObjectStore::remove_stale_startup_probes`] removes a probe key.
///
/// A [`crate::startup_self_check`] holds its probe for at most
/// [`STARTUP_PROBE_MAXIMUM_LIFETIME`], far less than this age.
pub const STALE_STARTUP_PROBE_AGE: Duration = Duration::from_secs(60 * 60);

// The margin also covers clock rate differences between the host and the store.
const _: () =
    assert!(STALE_STARTUP_PROBE_AGE.as_secs() >= 10 * STARTUP_PROBE_MAXIMUM_LIFETIME.as_secs());

/// Keys listed and deleted per request, the limit of [`ObjectStore::delete_many`].
const PROBE_PAGE_KEYS: u32 = 1000;

/// Deletes every version of the probe key `logical_key`.
pub(super) async fn purge_startup_probe(
    inner: &dyn ObjectStore,
    logical_key: &str,
) -> ObjectStoreResult<()> {
    let operation = ObjectStoreOperation::Delete;
    let prefix = version_prefix(logical_key);
    let mut start_after = None;
    loop {
        let listing = inner
            .list(&prefix, start_after.as_deref(), PROBE_PAGE_KEYS)
            .await
            .map_err(|error| inner_error(operation, logical_key, &error))?;
        let keys: Vec<String> = listing
            .objects
            .into_iter()
            .map(|object| object.key)
            .collect();
        if !keys.is_empty() {
            inner
                .delete_many(&keys)
                .await
                .map_err(|error| inner_error(operation, logical_key, &error))?;
        }
        match listing.next_start_after {
            Some(next) => start_after = Some(next),
            None => return Ok(()),
        }
    }
}

/// Returns the store's time: the last-write time of a new `clock_key` object, deleted again.
pub(super) async fn read_store_time(
    store: &dyn ObjectStore,
    clock_key: &str,
) -> ObjectStoreResult<i64> {
    let operation = ObjectStoreOperation::Put;
    let created = store
        .put(clock_key, Bytes::new(), WriteCondition::CreateOnly)
        .await?;
    if created == WriteOutcome::PreconditionFailed {
        return Err(ObjectStoreError::new(
            operation,
            clock_key,
            ObjectStoreErrorKind::Transient,
            "the store clock key already exists",
        ));
    }
    let written_at = store
        .head(clock_key)
        .await?
        .and_then(|metadata| metadata.last_modified_unix_micros);
    store.delete(clock_key).await?;
    written_at.ok_or_else(|| {
        ObjectStoreError::new(
            operation,
            clock_key,
            ObjectStoreErrorKind::Permanent,
            "the store reports no last-write time, so probe ages are unknown",
        )
    })
}

/// Deletes probe keys whose newest object predates `cutoff_unix_micros`; returns their count.
pub(super) async fn remove_startup_probes_written_before(
    inner: &dyn ObjectStore,
    cutoff_unix_micros: i64,
) -> ObjectStoreResult<u64> {
    let operation = ObjectStoreOperation::Delete;
    let mut probe_objects = Vec::new();
    let mut start_after = None;
    loop {
        let listing = inner
            .list(
                STARTUP_PROBE_OBJECT_KEY_PREFIX,
                start_after.as_deref(),
                PROBE_PAGE_KEYS,
            )
            .await
            .map_err(|error| inner_error(operation, STARTUP_PROBE_OBJECT_KEY_PREFIX, &error))?;
        probe_objects.extend(listing.objects);
        match listing.next_start_after {
            Some(next) => start_after = Some(next),
            None => break,
        }
    }
    let mut stale_keys = Vec::new();
    let mut removed_probes = 0;
    for probe in group_by_probe_key(probe_objects) {
        // A listing returns the newest version of a probe key first.
        let is_stale = probe.first().is_some_and(|newest| {
            newest
                .last_modified_unix_micros
                .is_some_and(|written_at| written_at < cutoff_unix_micros)
        });
        if is_stale {
            removed_probes += 1;
            stale_keys.extend(probe.into_iter().map(|object| object.key));
        }
    }
    for batch in stale_keys.chunks(PROBE_PAGE_KEYS as usize) {
        inner
            .delete_many(batch)
            .await
            .map_err(|error| inner_error(operation, STARTUP_PROBE_OBJECT_KEY_PREFIX, &error))?;
    }
    Ok(removed_probes)
}

/// Groups a key-ordered listing by probe key; non-version objects form their own groups.
fn group_by_probe_key(objects: Vec<ObjectMetadata>) -> Vec<Vec<ObjectMetadata>> {
    let mut groups: Vec<(String, Vec<ObjectMetadata>)> = Vec::new();
    for object in objects {
        let probe_key = versioned_logical_key(&object.key)
            .unwrap_or(&object.key)
            .to_owned();
        match groups.last_mut() {
            Some((last_key, members)) if *last_key == probe_key => members.push(object),
            _ => groups.push((probe_key, vec![object])),
        }
    }
    groups.into_iter().map(|(_, members)| members).collect()
}
