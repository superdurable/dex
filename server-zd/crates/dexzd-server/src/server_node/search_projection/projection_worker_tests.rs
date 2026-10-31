// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Mutex;

use async_trait::async_trait;
use dexzd_keyspace::PartitionId;

use dexzd_search::sql_visibility::ProjectionError;

use super::{PartitionRebuilder, SearchProjectionError, retry_partition_rebuild};

struct RecoveringRebuilder {
    attempts: u64,
    finished: Mutex<Option<(PartitionId, u64)>>,
}

#[async_trait]
impl PartitionRebuilder for RecoveringRebuilder {
    fn last_sequence(&self) -> u64 {
        self.attempts
    }

    async fn attempt_rebuild(
        &mut self,
        _partition: PartitionId,
    ) -> Result<(), SearchProjectionError> {
        self.attempts += 1;
        if self.attempts < 3 {
            return Err(SearchProjectionError::CommittedState {
                detail: "search backend unavailable".into(),
            });
        }
        Ok(())
    }

    fn finish_rebuild(&self, partition: PartitionId, covered_sequence: u64) {
        *self
            .finished
            .lock()
            .expect("fake rebuild record is unlocked") = Some((partition, covered_sequence));
    }
}

#[tokio::test(start_paused = true)]
async fn rebuild_retries_until_the_search_backend_recovers() {
    let partition = PartitionId(3);
    let mut rebuilder = RecoveringRebuilder {
        attempts: 0,
        finished: Mutex::new(None),
    };
    let started = tokio::time::Instant::now();
    retry_partition_rebuild(&mut rebuilder, partition)
        .await
        .expect("the backend recovers");
    assert_eq!(rebuilder.attempts, 3);
    assert_eq!(
        tokio::time::Instant::now() - started,
        std::time::Duration::from_millis(300)
    );
    assert_eq!(
        *rebuilder
            .finished
            .lock()
            .expect("fake rebuild record is unlocked"),
        Some((partition, 2))
    );
}

struct PermanentlyFailingRebuilder {
    attempts: u64,
}

#[async_trait]
impl PartitionRebuilder for PermanentlyFailingRebuilder {
    fn last_sequence(&self) -> u64 {
        1
    }

    async fn attempt_rebuild(
        &mut self,
        _partition: PartitionId,
    ) -> Result<(), SearchProjectionError> {
        self.attempts += 1;
        Err(ProjectionError::InvalidState("invalid search document".into()).into())
    }

    fn finish_rebuild(&self, _partition: PartitionId, _covered_sequence: u64) {
        panic!("a failed partition cannot become searchable");
    }
}

#[tokio::test]
async fn permanent_rebuild_failure_does_not_retry() {
    let mut rebuilder = PermanentlyFailingRebuilder { attempts: 0 };
    let error = retry_partition_rebuild(&mut rebuilder, PartitionId(3))
        .await
        .expect_err("invalid committed state cannot recover by retrying");
    assert!(error.to_string().contains("invalid search document"));
    assert_eq!(rebuilder.attempts, 1);
}
