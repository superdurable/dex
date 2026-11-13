// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Conditional writes and deletes of mutable keys: create the next generation, then confirm it.

use bytes::Bytes;
use dexzd_metrics::VersionedListingStaleSite;
use tokio::time::Instant;

use crate::aliyun_oss::VersionIdSource;
use crate::aliyun_oss::version_body::{VersionEtag, VersionHeader};
use crate::aliyun_oss::version_discovery::{NewestVersionLocator, VersionDiscovery};
use crate::aliyun_oss::version_keys::{version_generation, version_object_key, version_prefix};
use crate::aliyun_oss::version_reads::{VersionReadExtent, inner_error, read_version_header};
use crate::aliyun_oss::version_reclamation::VersionReclaimer;
use crate::{
    ObjectStore, ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult,
    WriteCondition, WriteOutcome,
};

/// Bounds the tombstone attempts of one delete; each retry means another write won meanwhile.
const MAX_TOMBSTONE_ATTEMPTS: usize = 16;

/// Writes versions of mutable object keys to the inner store.
pub(super) struct VersionedObjectWriter<'a> {
    pub(super) inner: &'a dyn ObjectStore,
    pub(super) version_ids: &'a dyn VersionIdSource,
    pub(super) reclaimer: &'a VersionReclaimer,
    pub(super) locator: &'a NewestVersionLocator,
}

impl VersionedObjectWriter<'_> {
    /// Writes `payload` at `logical_key` if `condition` holds, with the outcomes of
    /// [`ObjectStore::put`].
    pub(super) async fn put(
        &self,
        logical_key: &str,
        payload: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        match condition {
            WriteCondition::Unconditional => Err(ObjectStoreError::new(
                ObjectStoreOperation::Put,
                logical_key,
                ObjectStoreErrorKind::Permanent,
                "a mutable object key accepts only CreateOnly and ReplaceIfEtagMatches writes",
            )),
            WriteCondition::CreateOnly => self.create(logical_key, payload).await,
            WriteCondition::ReplaceIfEtagMatches { etag } => {
                self.replace(logical_key, payload, &etag).await
            }
        }
    }

    /// Writes a tombstone over the current version; an absent or deleted key needs no write.
    pub(super) async fn delete(&self, logical_key: &str) -> ObjectStoreResult<()> {
        let operation = ObjectStoreOperation::Delete;
        for _ in 0..MAX_TOMBSTONE_ATTEMPTS {
            let read_started_at = Instant::now();
            let Some(newest) = self
                .locator
                .read_newest_version(operation, logical_key, VersionReadExtent::HeaderOnly)
                .await?
            else {
                return Ok(());
            };
            if newest.header.is_tombstone {
                return Ok(());
            }
            let outcome = self
                .create_version(
                    operation,
                    logical_key,
                    next_generation(operation, logical_key, newest.generation)?,
                    Some(newest.header.version_id),
                    None,
                    read_started_at,
                )
                .await?;
            if matches!(outcome, WriteOutcome::Written { .. }) {
                return Ok(());
            }
        }
        Err(ObjectStoreError::new(
            operation,
            logical_key,
            ObjectStoreErrorKind::Transient,
            "concurrent writes superseded every tombstone attempt",
        ))
    }

    /// Creates the key when it is absent or its newest version is a tombstone.
    async fn create(&self, logical_key: &str, payload: Bytes) -> ObjectStoreResult<WriteOutcome> {
        let operation = ObjectStoreOperation::Put;
        let read_started_at = Instant::now();
        let newest = self
            .locator
            .read_newest_version(operation, logical_key, VersionReadExtent::HeaderOnly)
            .await?;
        let (generation, predecessor_id) = match newest {
            None => (1, None),
            Some(newest) if newest.header.is_tombstone => (
                next_generation(operation, logical_key, newest.generation)?,
                Some(newest.header.version_id),
            ),
            Some(_) => return Ok(WriteOutcome::PreconditionFailed),
        };
        self.create_version(
            operation,
            logical_key,
            generation,
            predecessor_id,
            Some(payload),
            read_started_at,
        )
        .await
    }

    /// Replaces the version named by `etag` when a bound read finds it as the newest version.
    async fn replace(
        &self,
        logical_key: &str,
        payload: Bytes,
        etag: &str,
    ) -> ObjectStoreResult<WriteOutcome> {
        let operation = ObjectStoreOperation::Put;
        let Some(expected) = VersionEtag::parse(etag) else {
            return Ok(WriteOutcome::PreconditionFailed);
        };
        // Only the newest version gets a successor; a hidden or forged version never does.
        let read_started_at = Instant::now();
        let newest = self
            .locator
            .read_newest_version(operation, logical_key, VersionReadExtent::HeaderOnly)
            .await?;
        let is_expected_version = newest.is_some_and(|newest| {
            newest.generation == expected.generation
                && newest.header.version_id == expected.version_id
                && !newest.header.is_tombstone
        });
        if !is_expected_version {
            return Ok(WriteOutcome::PreconditionFailed);
        }
        self.create_version(
            operation,
            logical_key,
            next_generation(operation, logical_key, expected.generation)?,
            Some(expected.version_id),
            Some(payload),
            read_started_at,
        )
        .await
    }

    /// Creates and confirms `generation`, a tombstone without `payload`.
    ///
    /// `read_started_at` is when the read of the newest version before it started.
    ///
    /// Returns `OutcomeUnknown` for any failure after the create succeeded.
    async fn create_version(
        &self,
        operation: ObjectStoreOperation,
        logical_key: &str,
        generation: u64,
        predecessor_id: Option<u128>,
        payload: Option<Bytes>,
        read_started_at: Instant,
    ) -> ObjectStoreResult<WriteOutcome> {
        let header = VersionHeader {
            version_id: self.version_ids.next_version_id(),
            predecessor_id,
            is_tombstone: payload.is_none(),
        };
        let body = header.encode_with_payload(payload.as_deref().unwrap_or_default());
        let physical_key = version_object_key(logical_key, generation);
        match self
            .inner
            .put(&physical_key, body, WriteCondition::CreateOnly)
            .await
            .map_err(|error| inner_error(operation, logical_key, &error))?
        {
            WriteOutcome::PreconditionFailed => return Ok(WriteOutcome::PreconditionFailed),
            WriteOutcome::Written { .. } => {}
        }
        let outcome = match self.locator.discovery {
            VersionDiscovery::Listing => {
                self.confirm_created_version(operation, logical_key, generation, header.version_id)
                    .await?
            }
            VersionDiscovery::HeadProbing => {
                self.confirm_created_version_by_successor(
                    operation,
                    logical_key,
                    generation,
                    header.version_id,
                    read_started_at,
                )
                .await?
            }
        };
        if matches!(outcome, WriteOutcome::Written { .. }) {
            // Every newer generation is written after the read that preceded this create.
            self.locator
                .remember_created_generation(logical_key, generation, read_started_at);
            self.reclaimer.reclaim_below(logical_key, generation);
        }
        Ok(outcome)
    }

    /// Confirms `generation` by reading the next generation instead of listing.
    ///
    /// Returns `Written` when the next generation is missing and the create was too quick to
    /// recreate a reclaimed generation, or when the next generation names this version. Returns
    /// `PreconditionFailed` after deleting a resurrected version, and `OutcomeUnknown` when a
    /// slow create leaves a missing next generation ambiguous.
    async fn confirm_created_version_by_successor(
        &self,
        operation: ObjectStoreOperation,
        logical_key: &str,
        generation: u64,
        version_id: u128,
        read_started_at: Instant,
    ) -> ObjectStoreResult<WriteOutcome> {
        let Some(next_generation) = generation.checked_add(1) else {
            return Ok(written(generation, version_id));
        };
        let successor = read_version_header(self.inner, operation, logical_key, next_generation)
            .await
            .map_err(|error| unconfirmed(operation, logical_key, &error.to_string()))?;
        match successor {
            Some(successor) => {
                self.check_successor(logical_key, generation, version_id, &successor)
                    .await
            }
            // The read found `generation - 1` newest, so a reclaimed `generation` needs the age.
            None if self.locator.is_too_recent_for_resurrection(read_started_at) => {
                Ok(written(generation, version_id))
            }
            None => Err(unconfirmed(
                operation,
                logical_key,
                "the create took too long to tell a new version from a recreated reclaimed one",
            )),
        }
    }

    /// Returns `Written` when `successor` names this version as its predecessor, and otherwise
    /// `PreconditionFailed` after deleting this resurrected version.
    async fn check_successor(
        &self,
        logical_key: &str,
        generation: u64,
        version_id: u128,
        successor: &VersionHeader,
    ) -> ObjectStoreResult<WriteOutcome> {
        if successor.predecessor_id == Some(version_id) {
            return Ok(written(generation, version_id));
        }
        let physical_key = version_object_key(logical_key, generation);
        if let Err(error) = self.inner.delete(&physical_key).await {
            tracing::warn!(
                key = %physical_key,
                %error,
                "a resurrected version could not be deleted; reclamation removes it later"
            );
        }
        Ok(WriteOutcome::PreconditionFailed)
    }

    /// Returns `Written` for a real version, and `PreconditionFailed` after deleting a resurrected
    /// one.
    async fn confirm_created_version(
        &self,
        operation: ObjectStoreOperation,
        logical_key: &str,
        generation: u64,
        version_id: u128,
    ) -> ObjectStoreResult<WriteOutcome> {
        let written = WriteOutcome::Written {
            etag: VersionEtag {
                generation,
                version_id,
            }
            .to_string(),
        };
        let prefix = version_prefix(logical_key);
        let listing = self
            .inner
            .list(&prefix, None, 1)
            .await
            .map_err(|error| unconfirmed(operation, logical_key, &error.to_string()))?;
        let newest_generation = listing
            .objects
            .first()
            .and_then(|newest| version_generation(&newest.key, &prefix));
        let Some(newest_generation) = newest_generation else {
            self.locator.record_stale_listing(
                VersionedListingStaleSite::ConfirmCreatedVersion,
                logical_key,
                generation,
                None,
            );
            return Err(unconfirmed(
                operation,
                logical_key,
                "the listing after the create shows no well-formed version",
            ));
        };
        if newest_generation == generation {
            return Ok(written);
        }
        if newest_generation < generation {
            self.locator.record_stale_listing(
                VersionedListingStaleSite::ConfirmCreatedVersion,
                logical_key,
                generation,
                Some(newest_generation),
            );
            return Err(unconfirmed(
                operation,
                logical_key,
                "the listing after the create does not show the created version",
            ));
        }
        // A newer generation exists, so `generation` is below `u64::MAX`.
        let successor = read_version_header(self.inner, operation, logical_key, generation + 1)
            .await
            .map_err(|error| unconfirmed(operation, logical_key, &error.to_string()))?;
        let Some(successor) = successor else {
            return Err(unconfirmed(
                operation,
                logical_key,
                "the next generation was reclaimed before the created version could be confirmed",
            ));
        };
        if successor.predecessor_id == Some(version_id) {
            return Ok(written);
        }
        let physical_key = version_object_key(logical_key, generation);
        if let Err(error) = self.inner.delete(&physical_key).await {
            tracing::warn!(
                key = %physical_key,
                %error,
                "a resurrected version could not be deleted; reclamation removes it later"
            );
        }
        Ok(WriteOutcome::PreconditionFailed)
    }
}

/// Returns the outcome of a confirmed write of `generation` with `version_id`.
fn written(generation: u64, version_id: u128) -> WriteOutcome {
    WriteOutcome::Written {
        etag: VersionEtag {
            generation,
            version_id,
        }
        .to_string(),
    }
}

/// Returns the generation after `generation`, or a permanent error when none exists.
fn next_generation(
    operation: ObjectStoreOperation,
    logical_key: &str,
    generation: u64,
) -> ObjectStoreResult<u64> {
    generation.checked_add(1).ok_or_else(|| {
        ObjectStoreError::new(
            operation,
            logical_key,
            ObjectStoreErrorKind::Permanent,
            "the key has used every generation",
        )
    })
}

fn unconfirmed(
    operation: ObjectStoreOperation,
    logical_key: &str,
    detail: &str,
) -> ObjectStoreError {
    ObjectStoreError::new(
        operation,
        logical_key,
        ObjectStoreErrorKind::OutcomeUnknown,
        format!("the created version could not be confirmed: {detail}"),
    )
}
