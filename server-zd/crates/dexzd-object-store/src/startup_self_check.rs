// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

use crate::{
    ObjectStore, ObjectStoreError, STARTUP_PROBE_OBJECT_KEY_PREFIX, WriteCondition, WriteOutcome,
};
use bytes::Bytes;
use tokio::time::Instant;

/// Longest time [`startup_self_check`] runs before it fails, so a slow store fails startup.
pub const STARTUP_SELF_CHECK_DEADLINE: Duration = Duration::from_secs(5 * 60);

/// Longest time the cleanup after a failed or overrun check may take.
const PROBE_CLEANUP_LIMIT: Duration = Duration::from_secs(60);

/// Longest time a running [`startup_self_check`] holds its probe; suspension extends it.
pub const STARTUP_PROBE_MAXIMUM_LIFETIME: Duration =
    Duration::from_secs(STARTUP_SELF_CHECK_DEADLINE.as_secs() + PROBE_CLEANUP_LIMIT.as_secs());

/// The conditional-write guarantee that failed during startup.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StartupSelfCheckFailure {
    /// The initial create did not succeed.
    InitialCreate,
    /// A duplicate create was accepted or failed unexpectedly.
    DuplicateCreate,
    /// A replacement with an incorrect ETag was accepted or failed unexpectedly.
    WrongEtagReplacement,
    /// A replacement with the current ETag was rejected or failed unexpectedly.
    CorrectEtagReplacement,
    /// The probe object could not be removed.
    Cleanup,
}

/// Failure of an object-store startup probe.
#[derive(Debug, thiserror::Error)]
#[error("object-store startup check {check:?} failed: {detail}")]
pub struct StartupSelfCheckError {
    /// The failed check.
    pub check: StartupSelfCheckFailure,
    /// Description of the observed result.
    pub detail: String,
    /// Store error, if the operation failed.
    #[source]
    pub source: Option<ObjectStoreError>,
}

/// Verifies create-only and ETag replacement semantics, then removes its probe object.
///
/// The check fails when it overruns [`STARTUP_SELF_CHECK_DEADLINE`]; the failure names the
/// step in progress.
pub async fn startup_self_check(store: &dyn ObjectStore) -> Result<(), StartupSelfCheckError> {
    let key = format!(
        "{STARTUP_PROBE_OBJECT_KEY_PREFIX}{}-{}",
        std::process::id(),
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .expect("system clock is after the Unix epoch")
            .as_nanos()
    );
    let deadline = Instant::now() + STARTUP_SELF_CHECK_DEADLINE;
    let mut step = StartupSelfCheckFailure::InitialCreate;
    let checked =
        tokio::time::timeout_at(deadline, check_conditional_writes(store, &key, &mut step)).await;
    let mut observed = match checked {
        Ok(Ok(())) => {
            return match tokio::time::timeout_at(deadline, store.delete(&key)).await {
                Ok(deleted) => deleted.map_err(|source| StartupSelfCheckError {
                    check: StartupSelfCheckFailure::Cleanup,
                    detail: "deleting probe failed".to_owned(),
                    source: Some(source),
                }),
                Err(_) => Err(overrun(StartupSelfCheckFailure::Cleanup)),
            };
        }
        Ok(Err(observed)) => observed,
        Err(_) => overrun(step),
    };
    match tokio::time::timeout(PROBE_CLEANUP_LIMIT, store.delete(&key)).await {
        Ok(Ok(())) => {}
        Ok(Err(cleanup_error)) => observed
            .detail
            .push_str(&format!("; cleanup failed: {cleanup_error}")),
        Err(_) => observed.detail.push_str(&format!(
            "; cleanup exceeded its {PROBE_CLEANUP_LIMIT:?} limit"
        )),
    }
    Err(observed)
}

/// Runs the four conditional writes on `key`, recording the step in progress in `step`.
async fn check_conditional_writes(
    store: &dyn ObjectStore,
    key: &str,
    step: &mut StartupSelfCheckFailure,
) -> Result<(), StartupSelfCheckError> {
    *step = StartupSelfCheckFailure::InitialCreate;
    let first = store
        .put(
            key,
            Bytes::from_static(b"probe"),
            WriteCondition::CreateOnly,
        )
        .await;
    let initial_etag = match first {
        Ok(WriteOutcome::Written { etag }) => etag,
        other => return Err(failure(StartupSelfCheckFailure::InitialCreate, other)),
    };
    *step = StartupSelfCheckFailure::DuplicateCreate;
    let duplicate = store
        .put(
            key,
            Bytes::from_static(b"duplicate"),
            WriteCondition::CreateOnly,
        )
        .await;
    if !matches!(duplicate, Ok(WriteOutcome::PreconditionFailed)) {
        return Err(failure(StartupSelfCheckFailure::DuplicateCreate, duplicate));
    }
    *step = StartupSelfCheckFailure::WrongEtagReplacement;
    let replacement = store
        .put(
            key,
            Bytes::from_static(b"replacement"),
            WriteCondition::ReplaceIfEtagMatches {
                etag: "\"wrong-etag\"".to_owned(),
            },
        )
        .await;
    if !matches!(replacement, Ok(WriteOutcome::PreconditionFailed)) {
        return Err(failure(
            StartupSelfCheckFailure::WrongEtagReplacement,
            replacement,
        ));
    }
    *step = StartupSelfCheckFailure::CorrectEtagReplacement;
    let correct_replacement = store
        .put(
            key,
            Bytes::from_static(b"replacement"),
            WriteCondition::ReplaceIfEtagMatches { etag: initial_etag },
        )
        .await;
    if !matches!(correct_replacement, Ok(WriteOutcome::Written { .. })) {
        return Err(failure(
            StartupSelfCheckFailure::CorrectEtagReplacement,
            correct_replacement,
        ));
    }
    *step = StartupSelfCheckFailure::Cleanup;
    Ok(())
}

fn overrun(check: StartupSelfCheckFailure) -> StartupSelfCheckError {
    StartupSelfCheckError {
        check,
        detail: format!("the check exceeded its {STARTUP_SELF_CHECK_DEADLINE:?} deadline"),
        source: None,
    }
}

fn failure(
    check: StartupSelfCheckFailure,
    result: Result<WriteOutcome, ObjectStoreError>,
) -> StartupSelfCheckError {
    match result {
        Ok(outcome) => StartupSelfCheckError {
            check,
            detail: format!("unexpected outcome: {outcome:?}"),
            source: None,
        },
        Err(source) => StartupSelfCheckError {
            check,
            detail: "storage operation failed".to_owned(),
            source: Some(source),
        },
    }
}
