// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A racing read-modify-write workload on one key, and a linearizability check of its history.

use std::collections::BTreeMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use bytes::Bytes;
use dexzd_object_store::{
    AliyunOssObjectStore, ObjectStore, ObjectStoreError, ObjectStoreErrorKind, WriteCondition,
    WriteOutcome,
};

use crate::create_only_store::{CreateOnlyStore, etag_generation};

const READER_COUNT: usize = 2;
const READS_PER_READER: usize = 30;

/// What one write reported.
#[derive(Debug, Clone, PartialEq, Eq)]
enum RecordedWriteOutcome {
    Written {
        etag: String,
    },
    /// `PreconditionFailed`, or an error that means no effect.
    NotWritten,
    Unknown,
}

#[derive(Debug, Clone)]
enum RecordedCall {
    Read {
        observed: Option<(String, Bytes)>,
    },
    Write {
        body: Bytes,
        outcome: RecordedWriteOutcome,
    },
}

/// One completed call, ordered by a global sequence taken when it started and finished.
#[derive(Debug, Clone)]
struct RecordedOperation {
    started: u64,
    finished: u64,
    call: RecordedCall,
}

/// Every call of one workload run.
pub(crate) struct ReplacementHistory {
    operations: Vec<RecordedOperation>,
}

struct HistoryRecorder {
    sequence: AtomicU64,
    operations: Mutex<Vec<RecordedOperation>>,
}

impl HistoryRecorder {
    fn start(&self) -> u64 {
        self.sequence.fetch_add(1, Ordering::SeqCst)
    }

    fn finish(&self, started: u64, call: RecordedCall) {
        let finished = self.sequence.fetch_add(1, Ordering::SeqCst);
        self.operations
            .lock()
            .expect("history lock is not poisoned")
            .push(RecordedOperation {
                started,
                finished,
                call,
            });
    }

    /// Reads `key`, recording the read unless it failed transiently.
    async fn read(
        &self,
        store: &dyn ObjectStore,
        key: &str,
    ) -> Result<Option<(String, Bytes)>, ObjectStoreError> {
        let started = self.start();
        let observed = match store.get(key).await {
            Ok(content) => content.map(|content| (content.etag, content.bytes)),
            Err(error) => {
                assert_eq!(error.kind, ObjectStoreErrorKind::Transient, "{error}");
                return Err(error);
            }
        };
        self.finish(
            started,
            RecordedCall::Read {
                observed: observed.clone(),
            },
        );
        Ok(observed)
    }
}

/// How many tasks race, and how they pace themselves.
#[derive(Debug, Clone, Copy)]
pub(crate) struct ReplacementWorkload {
    /// Tasks that read and conditionally replace the key.
    pub(crate) writers: usize,
    /// Read-and-replace attempts of each writer.
    pub(crate) attempts: usize,
    /// Longest pause of a writer between attempts.
    pub(crate) maximum_pause: Duration,
}

/// Runs the writers of `workload` and readers on `key`, each through its own adapter over
/// `physical`.
pub(crate) async fn run_replacement_workload(
    physical: &CreateOnlyStore,
    key: &'static str,
    workload: ReplacementWorkload,
) -> ReplacementHistory {
    let ReplacementWorkload {
        writers,
        attempts,
        maximum_pause,
    } = workload;
    let recorder = Arc::new(HistoryRecorder {
        sequence: AtomicU64::new(0),
        operations: Mutex::new(Vec::new()),
    });
    let mut tasks = Vec::new();
    for writer in 0..writers {
        let store = AliyunOssObjectStore::new(physical.inner());
        let recorder = Arc::clone(&recorder);
        let task = tokio::spawn(async move {
            for attempt in 0..attempts {
                let Ok(current) = recorder.read(&store, key).await else {
                    continue;
                };
                let expected_etag = current.map(|(etag, _)| etag);
                let body = Bytes::from(format!(
                    "writer {writer} attempt {attempt} expects {}",
                    expected_etag.as_deref().unwrap_or("none")
                ));
                let condition = match expected_etag {
                    Some(etag) => WriteCondition::ReplaceIfEtagMatches { etag },
                    None => WriteCondition::CreateOnly,
                };
                let started = recorder.start();
                let outcome = match store.put(key, body.clone(), condition).await {
                    Ok(WriteOutcome::Written { etag }) => RecordedWriteOutcome::Written { etag },
                    Ok(WriteOutcome::PreconditionFailed) => RecordedWriteOutcome::NotWritten,
                    Err(error) if error.kind == ObjectStoreErrorKind::OutcomeUnknown => {
                        RecordedWriteOutcome::Unknown
                    }
                    Err(error) => {
                        assert_eq!(error.kind, ObjectStoreErrorKind::Transient, "{error}");
                        RecordedWriteOutcome::NotWritten
                    }
                };
                recorder.finish(started, RecordedCall::Write { body, outcome });
                let pause_range_millis =
                    u64::try_from(maximum_pause.as_millis()).expect("the pause fits in u64") + 1;
                let spread = u64::try_from(writer * 7919 + attempt * 104_729)
                    .expect("the spread fits in u64");
                tokio::time::sleep(Duration::from_millis(spread % pause_range_millis)).await;
            }
        });
        tasks.push(task);
    }
    for _ in 0..READER_COUNT {
        let store = AliyunOssObjectStore::new(physical.inner());
        let recorder = Arc::clone(&recorder);
        let task = tokio::spawn(async move {
            for _ in 0..READS_PER_READER {
                // A transient read failure has no effect to check.
                drop(recorder.read(&store, key).await);
                tokio::time::sleep(Duration::from_millis(7)).await;
            }
        });
        tasks.push(task);
    }
    for task in tasks {
        task.await.expect("a workload task finishes");
    }
    let store = AliyunOssObjectStore::new(physical.inner());
    recorder
        .read(&store, key)
        .await
        .expect("the final read succeeds");
    let operations = recorder
        .operations
        .lock()
        .expect("history lock is not poisoned")
        .clone();
    ReplacementHistory { operations }
}

impl ReplacementHistory {
    /// Returns the number of writes that reported `Written`.
    pub(crate) fn written_count(&self) -> usize {
        self.operations
            .iter()
            .filter(|operation| {
                matches!(
                    operation.call,
                    RecordedCall::Write {
                        outcome: RecordedWriteOutcome::Written { .. },
                        ..
                    }
                )
            })
            .count()
    }

    /// Checks that the history is a linearizable history of one compare-and-swap register.
    pub(crate) fn assert_linearizable(&self) {
        let versions = self.versions_by_generation();
        for (generation, (etag, body)) in &versions {
            let expected = expected_etag(body);
            if *generation == 1 {
                assert_eq!(expected, None, "generation 1 is a create");
                continue;
            }
            let expected = expected.expect("a later generation is a replacement");
            assert_eq!(etag_generation(&expected), generation - 1, "{etag}");
            if let Some((predecessor_etag, _)) = versions.get(&(generation - 1)) {
                assert_eq!(
                    &expected, predecessor_etag,
                    "generation {generation} replaced the previous winner"
                );
            }
        }
        let writes: Vec<(&RecordedOperation, &Bytes, &RecordedWriteOutcome)> = self
            .operations
            .iter()
            .filter_map(|operation| match &operation.call {
                RecordedCall::Write { body, outcome } => Some((operation, body, outcome)),
                RecordedCall::Read { .. } => None,
            })
            .collect();
        for (_, body, outcome) in &writes {
            if **outcome == RecordedWriteOutcome::NotWritten {
                assert!(
                    versions.values().all(|(_, observed)| observed != *body),
                    "a write reported as not written was observed: {body:?}"
                );
            }
        }
        let reads: Vec<(&RecordedOperation, u64, Option<&Bytes>)> = self
            .operations
            .iter()
            .filter_map(|operation| match &operation.call {
                RecordedCall::Read { observed } => Some((
                    operation,
                    observed
                        .as_ref()
                        .map_or(0, |(etag, _)| etag_generation(etag)),
                    observed.as_ref().map(|(_, body)| body),
                )),
                RecordedCall::Write { .. } => None,
            })
            .collect();
        for (read, observed_generation, observed_body) in &reads {
            for (write, body, outcome) in &writes {
                if let RecordedWriteOutcome::Written { etag } = outcome
                    && write.finished < read.started
                {
                    assert!(
                        *observed_generation >= etag_generation(etag),
                        "a read after a confirmed write observed an older version"
                    );
                }
                if Some(*body) == *observed_body {
                    assert!(
                        write.started < read.finished,
                        "a read observed a write that had not started"
                    );
                }
            }
            for (earlier, earlier_generation, _) in &reads {
                if earlier.finished < read.started {
                    assert!(
                        observed_generation >= earlier_generation,
                        "reads went backwards"
                    );
                }
            }
        }
        let final_read = reads
            .iter()
            .max_by_key(|(read, _, _)| read.started)
            .expect("the workload ends with a read");
        assert_eq!(
            Some(&final_read.1),
            versions.keys().next_back(),
            "the final read observes the newest version"
        );
    }

    /// Returns each generation's ETag and body, asserting that all observations agree.
    fn versions_by_generation(&self) -> BTreeMap<u64, (String, Bytes)> {
        let mut versions: BTreeMap<u64, (String, Bytes)> = BTreeMap::new();
        for operation in &self.operations {
            let observation = match &operation.call {
                RecordedCall::Read {
                    observed: Some((etag, body)),
                } => (etag.clone(), body.clone()),
                RecordedCall::Write {
                    body,
                    outcome: RecordedWriteOutcome::Written { etag },
                } => (etag.clone(), body.clone()),
                _ => continue,
            };
            let generation = etag_generation(&observation.0);
            let stored = versions
                .entry(generation)
                .or_insert_with(|| observation.clone());
            assert_eq!(
                *stored, observation,
                "generation {generation} has exactly one winner"
            );
        }
        versions
    }
}

/// Returns the ETag a workload body was written against.
fn expected_etag(body: &Bytes) -> Option<String> {
    let text = std::str::from_utf8(body).expect("workload bodies are UTF-8");
    let (_, expected) = text
        .split_once(" expects ")
        .expect("workload bodies name their expected ETag");
    (expected != "none").then(|| expected.to_owned())
}
