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

use crate::aliyun_oss::VersionIdSource;
use crate::aliyun_oss::version_body::VersionHeader;
use crate::aliyun_oss::version_keys::{version_object_key, version_prefix, versioned_logical_key};
use crate::aliyun_oss::version_reads::inner_error;
use crate::{
    ObjectMetadata, ObjectStore, ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation,
    ObjectStoreResult, STARTUP_PROBE_MAXIMUM_LIFETIME, STARTUP_PROBE_OBJECT_KEY_PREFIX,
    WriteCondition, WriteOutcome,
};

/// Store-clock age after which the stale probe sweep removes a probe key: 24 hours.
pub const STALE_STARTUP_PROBE_AGE: Duration = Duration::from_secs(24 * 60 * 60);

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

/// Returns the store's time from a new `clock_key` object, or `None` if another sweep removed it.
pub(super) async fn read_store_time(
    store: &dyn ObjectStore,
    clock_key: &str,
) -> ObjectStoreResult<Option<i64>> {
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
    let Some(clock_object) = store.head(clock_key).await? else {
        return Ok(None);
    };
    store.delete(clock_key).await?;
    clock_object
        .last_modified_unix_micros
        .map(Some)
        .ok_or_else(|| {
            ObjectStoreError::new(
                operation,
                clock_key,
                ObjectStoreErrorKind::Permanent,
                "the store reports no last-write time, so probe ages are unknown",
            )
        })
}

/// Returns the store time of a clock probe version this call writes to `inner` and deletes.
///
/// The probe is generation 1 of a new `probe/store-clock-*` key, as the adapter would write it,
/// so the stale probe sweep removes it if the delete fails. The store sets its last-write time
/// during this call, so it is a lower bound of the store's clock when this call returns.
///
/// # Errors
///
/// Returns the inner store's error when the write or read fails, and
/// [`ObjectStoreErrorKind::Transient`] when the probe is missing or reports no last-write time.
pub(super) async fn read_store_time_of_new_probe_version(
    inner: &dyn ObjectStore,
    version_ids: &dyn VersionIdSource,
) -> ObjectStoreResult<i64> {
    let operation = ObjectStoreOperation::Put;
    let clock_key = format!(
        "{STARTUP_PROBE_OBJECT_KEY_PREFIX}store-clock-{:032x}",
        version_ids.next_version_id()
    );
    let physical_key = version_object_key(&clock_key, 1);
    let header = VersionHeader {
        version_id: version_ids.next_version_id(),
        predecessor_id: None,
        is_tombstone: false,
    };
    let created = inner
        .put(
            &physical_key,
            header.encode_with_payload(&[]),
            WriteCondition::CreateOnly,
        )
        .await
        .map_err(|error| inner_error(operation, &clock_key, &error))?;
    let written_at = match created {
        WriteOutcome::Written { .. } => inner
            .head(&physical_key)
            .await
            .map_err(|error| inner_error(operation, &clock_key, &error))?
            .and_then(|metadata| metadata.last_modified_unix_micros),
        WriteOutcome::PreconditionFailed => None,
    };
    if let Err(error) = purge_startup_probe(inner, &clock_key).await {
        tracing::warn!(
            key = %clock_key,
            %error,
            "deleting a store clock probe failed; the stale probe sweep removes it"
        );
    }
    written_at.ok_or_else(|| {
        ObjectStoreError::new(
            operation,
            &clock_key,
            ObjectStoreErrorKind::Transient,
            "the store clock probe reports no last-write time",
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
