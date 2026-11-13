// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A racing read-modify-write workload on one key, and a linearizability check of its history.

use std::collections::{BTreeMap, HashSet};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use bytes::Bytes;
use dexzd_metrics::NodeMetrics;
use dexzd_object_store::{
    AliyunOssObjectStore, ObjectStore, ObjectStoreError, ObjectStoreErrorKind, WriteCondition,
    WriteOutcome,
};

use crate::create_only_store::{CreateOnlyStore, TEST_MINIMUM_SUPERSEDED_AGE, etag_generation};

const READER_COUNT: usize = 2;
const READS_PER_READER: usize = 30;

/// What one write reported.
#[derive(Debug, Clone, PartialEq, Eq)]
enum RecordedWriteOutcome {
    Written {
        etag: String,
    },
    PreconditionFailed,
    /// An error that means the write had no effect.
    FailedWithoutEffect,
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
        let store = AliyunOssObjectStore::new(
            physical.inner(),
            TEST_MINIMUM_SUPERSEDED_AGE,
            NodeMetrics::unregistered(),
        );
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
                    Ok(WriteOutcome::PreconditionFailed) => {
                        RecordedWriteOutcome::PreconditionFailed
                    }
                    Err(error) if error.kind == ObjectStoreErrorKind::OutcomeUnknown => {
                        RecordedWriteOutcome::Unknown
                    }
                    Err(error) => {
                        assert_eq!(error.kind, ObjectStoreErrorKind::Transient, "{error}");
                        RecordedWriteOutcome::FailedWithoutEffect
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
        let store = AliyunOssObjectStore::new(
            physical.inner(),
            TEST_MINIMUM_SUPERSEDED_AGE,
            NodeMetrics::unregistered(),
        );
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
    let store = AliyunOssObjectStore::new(
        physical.inner(),
        TEST_MINIMUM_SUPERSEDED_AGE,
        NodeMetrics::unregistered(),
    );
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
    ///
    /// Each generation must have one winner that replaced the previous one. The calls must then
    /// fit a sequential register in real-time order; unknown outcomes may or may not apply.
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
        for operation in &self.operations {
            if let RecordedCall::Write {
                body,
                outcome:
                    RecordedWriteOutcome::PreconditionFailed | RecordedWriteOutcome::FailedWithoutEffect,
            } = &operation.call
            {
                assert!(
                    versions.values().all(|(_, observed)| observed != body),
                    "a write reported as not written was observed: {body:?}"
                );
            }
        }
        let calls = self.model_calls(&versions);
        assert!(
            is_linearizable(&calls),
            "the history fits no sequential compare-and-swap order"
        );
        let final_read = self
            .operations
            .iter()
            .filter_map(|operation| match &operation.call {
                RecordedCall::Read { observed } => Some((operation.started, observed)),
                RecordedCall::Write { .. } => None,
            })
            .max_by_key(|(started, _)| *started)
            .expect("the workload ends with a read");
        assert_eq!(
            final_read.1.as_ref().map(|(etag, _)| etag_generation(etag)),
            versions.keys().next_back().copied(),
            "the final read observes the newest version"
        );
    }

    /// Returns the calls of the sequential model, with bodies numbered, in start order.
    fn model_calls(&self, versions: &BTreeMap<u64, (String, Bytes)>) -> Vec<ModelCall> {
        let mut body_numbers: BTreeMap<Bytes, usize> = BTreeMap::new();
        for operation in &self.operations {
            if let RecordedCall::Write { body, .. } = &operation.call {
                let next_number = body_numbers.len();
                body_numbers.entry(body.clone()).or_insert(next_number);
            }
        }
        let body_number = |body: &Bytes| -> usize {
            *body_numbers
                .get(body)
                .unwrap_or_else(|| panic!("a read observed a body no write sent: {body:?}"))
        };
        let etag_bodies: BTreeMap<&str, &Bytes> = versions
            .values()
            .map(|(etag, body)| (etag.as_str(), body))
            .collect();
        let mut calls: Vec<ModelCall> = self
            .operations
            .iter()
            .filter_map(|operation| {
                let step = match &operation.call {
                    RecordedCall::Read { observed } => ModelStep::Read {
                        observed: observed.as_ref().map(|(_, body)| body_number(body)),
                    },
                    RecordedCall::Write { body, outcome } => {
                        let outcome = match outcome {
                            RecordedWriteOutcome::Written { .. } => ModelWriteOutcome::Written,
                            RecordedWriteOutcome::PreconditionFailed => {
                                ModelWriteOutcome::PreconditionFailed
                            }
                            RecordedWriteOutcome::Unknown => ModelWriteOutcome::Unknown,
                            RecordedWriteOutcome::FailedWithoutEffect => return None,
                        };
                        let expected = expected_etag(body).map(|etag| {
                            let expected_body =
                                etag_bodies.get(etag.as_str()).unwrap_or_else(|| {
                                    panic!("a write expected an ETag never observed: {etag}")
                                });
                            body_number(expected_body)
                        });
                        ModelStep::Write {
                            expected,
                            written: body_number(body),
                            outcome,
                        }
                    }
                };
                let is_unknown = matches!(
                    step,
                    ModelStep::Write {
                        outcome: ModelWriteOutcome::Unknown,
                        ..
                    }
                );
                Some(ModelCall {
                    started: operation.started,
                    finished: (!is_unknown).then_some(operation.finished),
                    step,
                })
            })
            .collect();
        calls.sort_by_key(|call| call.started);
        calls
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

/// Outcome of a write, as the sequential model sees it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum ModelWriteOutcome {
    Written,
    PreconditionFailed,
    /// The write may have applied at any time after it started, or never.
    Unknown,
}

/// One call on the register, whose values are numbered bodies.
#[derive(Debug, Clone, Copy)]
enum ModelStep {
    Read {
        observed: Option<usize>,
    },
    Write {
        /// The value the condition requires; `None` for a create.
        expected: Option<usize>,
        written: usize,
        outcome: ModelWriteOutcome,
    },
}

/// A call with its real-time interval; `finished` is `None` for an unknown outcome.
#[derive(Debug, Clone, Copy)]
struct ModelCall {
    started: u64,
    finished: Option<u64>,
    step: ModelStep,
}

/// Returns the register value after `step` applies to `state`, or `None` when it cannot apply.
fn apply_step(state: Option<usize>, step: ModelStep) -> Option<Option<usize>> {
    match step {
        ModelStep::Read { observed } => (state == observed).then_some(state),
        ModelStep::Write {
            expected,
            written,
            outcome,
        } => match outcome {
            ModelWriteOutcome::Written | ModelWriteOutcome::Unknown => {
                (state == expected).then_some(Some(written))
            }
            ModelWriteOutcome::PreconditionFailed => (state != expected).then_some(state),
        },
    }
}

/// Searches for a sequential order of `calls` that respects real time (Wing and Gong).
///
/// Every call with a known outcome must apply; an unknown write applies at most once.
fn is_linearizable(calls: &[ModelCall]) -> bool {
    let mut is_applied = vec![false; calls.len()];
    let mut visited = HashSet::new();
    search_linearization(calls, &mut is_applied, None, &mut visited)
}

fn search_linearization(
    calls: &[ModelCall],
    is_applied: &mut [bool],
    state: Option<usize>,
    visited: &mut HashSet<(Vec<u64>, Option<usize>)>,
) -> bool {
    let earliest_finish = calls
        .iter()
        .zip(is_applied.iter())
        .filter(|(_, is_applied)| !**is_applied)
        .filter_map(|(call, _)| call.finished)
        .min();
    let Some(earliest_finish) = earliest_finish else {
        return true;
    };
    // Calls are in start order; only calls started before the earliest pending finish can go next.
    for index in 0..calls.len() {
        let call = calls[index];
        if call.started >= earliest_finish {
            break;
        }
        if is_applied[index] {
            continue;
        }
        let Some(next_state) = apply_step(state, call.step) else {
            continue;
        };
        is_applied[index] = true;
        let applied_set = is_applied
            .chunks(64)
            .map(|chunk| {
                chunk.iter().enumerate().fold(0_u64, |bits, (bit, is_set)| {
                    bits | (u64::from(*is_set) << bit)
                })
            })
            .collect();
        if visited.insert((applied_set, next_state))
            && search_linearization(calls, is_applied, next_state, visited)
        {
            return true;
        }
        is_applied[index] = false;
    }
    false
}

fn write(
    started: u64,
    finished: u64,
    body: &str,
    outcome: RecordedWriteOutcome,
) -> RecordedOperation {
    RecordedOperation {
        started,
        finished,
        call: RecordedCall::Write {
            body: Bytes::from(body.to_owned()),
            outcome,
        },
    }
}

fn read(started: u64, finished: u64, observed: Option<(&str, &str)>) -> RecordedOperation {
    RecordedOperation {
        started,
        finished,
        call: RecordedCall::Read {
            observed: observed.map(|(etag, body)| (etag.to_owned(), Bytes::from(body.to_owned()))),
        },
    }
}

fn test_etag(generation: u64) -> String {
    format!("\"g{generation}-{generation:032x}\"")
}

fn written(generation: u64) -> RecordedWriteOutcome {
    RecordedWriteOutcome::Written {
        etag: test_etag(generation),
    }
}

#[test]
#[should_panic(expected = "fits no sequential")]
fn the_checker_rejects_a_precondition_failure_that_the_current_value_satisfied() {
    let first = "w0 a0 expects none";
    let rejected = format!("w0 a1 expects {}", test_etag(1));
    ReplacementHistory {
        operations: vec![
            write(0, 1, first, written(1)),
            write(2, 3, &rejected, RecordedWriteOutcome::PreconditionFailed),
            read(4, 5, Some((&test_etag(1), first))),
        ],
    }
    .assert_linearizable();
}

#[test]
#[should_panic(expected = "fits no sequential")]
fn the_checker_rejects_a_read_older_than_a_confirmed_replacement() {
    let first = "w0 a0 expects none";
    let second = format!("w0 a1 expects {}", test_etag(1));
    ReplacementHistory {
        operations: vec![
            write(0, 1, first, written(1)),
            write(2, 3, &second, written(2)),
            read(4, 5, Some((&test_etag(1), first))),
            read(6, 7, Some((&test_etag(2), &second))),
        ],
    }
    .assert_linearizable();
}

#[test]
#[should_panic(expected = "a later generation is a replacement")]
fn the_checker_rejects_two_creates_that_both_succeed() {
    let first = "w0 a0 expects none";
    let second_create = "w1 a0 expects none";
    ReplacementHistory {
        operations: vec![
            write(0, 1, first, written(1)),
            write(2, 3, second_create, written(2)),
            read(4, 5, Some((&test_etag(2), second_create))),
        ],
    }
    .assert_linearizable();
}

#[test]
#[should_panic(expected = "fits no sequential")]
fn the_checker_rejects_a_read_of_absence_after_a_create() {
    let first = "w0 a0 expects none";
    let second = format!("w1 a0 expects {}", test_etag(1));
    let late_create = "w2 a0 expects none";
    ReplacementHistory {
        operations: vec![
            write(0, 1, first, written(1)),
            write(2, 3, &second, written(2)),
            write(4, 5, late_create, RecordedWriteOutcome::Unknown),
            read(6, 7, Some((&test_etag(2), &second))),
            read(8, 9, None),
        ],
    }
    .assert_linearizable();
}

#[test]
#[should_panic(expected = "reported as not written")]
fn the_checker_rejects_a_read_of_a_rejected_write() {
    let first = "w0 a0 expects none";
    let rejected = format!("w1 a0 expects {}", test_etag(1));
    ReplacementHistory {
        operations: vec![
            write(0, 1, first, written(1)),
            write(2, 3, &rejected, RecordedWriteOutcome::PreconditionFailed),
            read(4, 5, Some((&test_etag(2), &rejected))),
        ],
    }
    .assert_linearizable();
}

#[test]
fn the_checker_accepts_overlapping_failures_and_an_unknown_write_that_applied() {
    let first = "w0 a0 expects none";
    let second = format!("w1 a0 expects {}", test_etag(1));
    let losing = format!("w2 a0 expects {}", test_etag(1));
    let unknown = format!("w3 a0 expects {}", test_etag(2));
    let never_applied = format!("w4 a0 expects {}", test_etag(2));
    ReplacementHistory {
        operations: vec![
            read(0, 1, None),
            write(2, 3, first, written(1)),
            read(4, 5, Some((&test_etag(1), first))),
            write(6, 14, &second, written(2)),
            write(7, 8, &losing, RecordedWriteOutcome::PreconditionFailed),
            read(15, 16, Some((&test_etag(2), &second))),
            write(17, 18, &unknown, RecordedWriteOutcome::Unknown),
            write(
                19,
                20,
                &never_applied,
                RecordedWriteOutcome::FailedWithoutEffect,
            ),
            read(21, 22, Some((&test_etag(3), &unknown))),
        ],
    }
    .assert_linearizable();
}
