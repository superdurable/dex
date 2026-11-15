// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! CloneForkFlow stagings and the FlowCloneFork command in apply: the staging cap, the checks
//! that bind a staging to its command, ID reuse conflicts, and copied hard-delete Attributes.

use super::*;
use dexzd_apply::{CloneForkComputation, CloneForkComputationRequest, ComputedCloneFork};
use dexzd_protocol::dex::{CloneForkFlowRequest, IdReusePolicy};
use dexzd_protocol::dexzd::v1::{
    FlowCloneForkCommand, FlowCloneForkStageCommand, FlowCloneForkStagedChunk,
    FlowCloneForkStagingExpireCommand, FlowCloneForkStagingIndexRecord, FlowDirectoryRecord,
    StoredValueId, TimeTravelStagedValue,
};

const CLONE_FLOW: &str = "clone";
const CLONE_RUN: [u8; 16] = [9; 16];

fn clone_request(request_id: &str) -> CloneForkFlowRequest {
    CloneForkFlowRequest {
        source_flow_id: "parent".to_owned(),
        flow_id: CLONE_FLOW.to_owned(),
        request_id: request_id.to_owned(),
        ..CloneForkFlowRequest::default()
    }
}

fn error_code(outcome: &ApplyOutcome) -> Option<i32> {
    match &outcome.reply.reply {
        Some(command_reply::Reply::Error(error)) => Some(error.grpc_code),
        _ => None,
    }
}

/// A state reader that counts the bytes its scans return.
struct ScanCountingState<'state> {
    state: &'state MemoryState,
    scanned_bytes: std::cell::Cell<usize>,
}

impl FlowStateReader for ScanCountingState<'_> {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        self.state.get(column_family, key)
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        let rows = self.state.scan(column_family, start, end, limit)?;
        let bytes: usize = rows
            .iter()
            .map(|(key, value)| key.len() + value.len())
            .sum();
        self.scanned_bytes.set(self.scanned_bytes.get() + bytes);
        Ok(rows)
    }
}

impl Harness {
    /// Computes the copy of the parent's current point as an owner does.
    fn compute_parent_copy(&self, request: &CloneForkFlowRequest) -> ComputedCloneFork {
        match self.compute_parent_copy_over(&self.state, request, u64::MAX) {
            CloneForkComputation::Computed(computed) => *computed,
            CloneForkComputation::Rejected(reply) => panic!("the copy is rejected: {reply:?}"),
        }
    }

    /// Computes the copy of the parent's current point over `state` within `memory_budget_bytes`.
    fn compute_parent_copy_over(
        &self,
        state: &dyn FlowStateReader,
        request: &CloneForkFlowRequest,
        memory_budget_bytes: u64,
    ) -> CloneForkComputation {
        let parent_hash = self.hasher.flow_hash(TenantId::DEFAULT, "parent");
        let context = ApplyContext {
            partition: self.hasher.partition_of(parent_hash),
            flow_hash: parent_hash,
        };
        let history = dexzd_apply::StateHistorySource::new(state, context, RunId(PARENT_RUN));
        let child_flow_runs = dexzd_apply::StateChildFlowRuns::new(state, &self.hasher);
        dexzd_apply::compute_clone_fork(
            &context,
            &dexzd_apply::TimeTravelComputationSources {
                state,
                history: &history,
                offloaded_values: &dexzd_apply::values::NoOffloadedValues,
                child_flow_runs: &child_flow_runs,
            },
            &CloneForkComputationRequest {
                source_run_id: RunId(PARENT_RUN),
                clone_fork: request,
                now_micros: self.now_micros,
                memory_budget_bytes,
            },
        )
        .expect("the clone fork computes")
    }

    fn stage_copy(&mut self, computed: &ComputedCloneFork, staging_id: &[u8]) {
        for stage in computed
            .stage_commands(staging_id)
            .expect("the copy fits in Stage commands")
        {
            assert_ok(&self.apply(CLONE_FLOW, &[], Command::FlowCloneForkStage(stage)));
        }
    }

    fn apply_clone_fork(&mut self, command: FlowCloneForkCommand) -> ApplyOutcome {
        self.apply(CLONE_FLOW, &[], Command::FlowCloneFork(command))
    }

    fn clone_fork_command(
        &self,
        request: CloneForkFlowRequest,
        staging_id: &[u8],
        new_run_id: [u8; 16],
    ) -> FlowCloneForkCommand {
        FlowCloneForkCommand {
            staging_id: staging_id.to_vec(),
            new_run_id: new_run_id.to_vec(),
            request: Some(request),
            settings: Some(settings()),
            effective_flow_config: self.record("parent", PARENT_RUN).flow_config,
            ..FlowCloneForkCommand::default()
        }
    }

    fn has_clone_staging(&self, staging_id: &[u8]) -> bool {
        let flow_hash = self.hasher.flow_hash(TenantId::DEFAULT, CLONE_FLOW);
        self.state
            .get(
                ColumnFamily::Flow,
                &dexzd_keyspace::row_keys::flow_clone_fork_staging_key(flow_hash, staging_id),
            )
            .expect("memory reads")
            .is_some()
    }
}

#[test]
fn a_partition_holds_at_most_64_stagings() {
    let mut harness = Harness::new();
    harness.start_parent();
    let computed = harness.compute_parent_copy(&clone_request("capped"));
    let chunk = computed
        .stage_commands(&[0; 16])
        .expect("the copy fits")
        .remove(0);
    for index in 0..64_u8 {
        assert_ok(&harness.apply(
            CLONE_FLOW,
            &[],
            Command::FlowCloneForkStage(dexzd_protocol::dexzd::v1::FlowCloneForkStageCommand {
                staging_id: vec![index; 16],
                ..chunk.clone()
            }),
        ));
    }
    let rows_before = harness.state.rows.clone();
    let rejected = harness.apply(
        CLONE_FLOW,
        &[],
        Command::FlowCloneForkStage(dexzd_protocol::dexzd::v1::FlowCloneForkStageCommand {
            staging_id: vec![64; 16],
            ..chunk.clone()
        }),
    );
    assert_eq!(error_code(&rejected), Some(8), "RESOURCE_EXHAUSTED");
    assert_eq!(harness.state.rows, rows_before);
    // A retry of a staging in progress replaces it rather than adding one.
    assert_ok(&harness.apply(
        CLONE_FLOW,
        &[],
        Command::FlowCloneForkStage(dexzd_protocol::dexzd::v1::FlowCloneForkStageCommand {
            staging_id: vec![3; 16],
            ..chunk
        }),
    ));
}

#[test]
fn a_staging_binds_to_its_request_and_must_be_complete() {
    let mut harness = Harness::new();
    harness.start_parent();
    let request = clone_request("bound");
    let computed = harness.compute_parent_copy(&request);
    let rows_before = harness.state.rows.clone();
    let missing =
        harness.apply_clone_fork(harness.clone_fork_command(request.clone(), &[1; 16], CLONE_RUN));
    assert_eq!(
        error_code(&missing),
        Some(10),
        "ABORTED for a missing staging"
    );
    assert_eq!(harness.state.rows, rows_before);
    harness.stage_copy(&computed, &[1; 16]);
    let staged_rows = harness.state.rows.clone();
    let other_request = CloneForkFlowRequest {
        reason: "another request".to_owned(),
        ..request.clone()
    };
    let mismatch =
        harness.apply_clone_fork(harness.clone_fork_command(other_request, &[1; 16], CLONE_RUN));
    assert_eq!(error_code(&mismatch), Some(3), "INVALID_ARGUMENT");
    assert_eq!(harness.state.rows, staged_rows);
    let applied =
        harness.apply_clone_fork(harness.clone_fork_command(request, &[1; 16], CLONE_RUN));
    assert!(matches!(
        applied.reply.reply,
        Some(command_reply::Reply::StartFlow(_))
    ));
    assert!(
        !harness.has_clone_staging(&[1; 16]),
        "the run consumed the staging"
    );
}

#[test]
fn an_id_reuse_conflict_answers_as_start_flow_and_deletes_the_staging() {
    let mut harness = Harness::new();
    harness.start_parent();
    let first = clone_request("first");
    let computed = harness.compute_parent_copy(&first);
    harness.stage_copy(&computed, &[1; 16]);
    assert!(matches!(
        harness
            .apply_clone_fork(harness.clone_fork_command(first, &[1; 16], CLONE_RUN))
            .reply
            .reply,
        Some(command_reply::Reply::StartFlow(_))
    ));
    let second = CloneForkFlowRequest {
        id_reuse_policy: IdReusePolicy::DisallowReuse as i32,
        ..clone_request("second")
    };
    let computed = harness.compute_parent_copy(&second);
    harness.stage_copy(&computed, &[2; 16]);
    let conflict = harness.apply_clone_fork(harness.clone_fork_command(second, &[2; 16], [8; 16]));
    let Some(command_reply::Reply::Error(error)) = &conflict.reply.reply else {
        panic!("the conflict is rejected: {:?}", conflict.reply);
    };
    assert_eq!(error.grpc_code, 6, "ALREADY_EXISTS");
    assert_eq!(
        error.sub_status,
        dexzd_protocol::dex::ErrorSubStatus::FlowAlreadyStarted as i32
    );
    assert!(
        !harness.has_clone_staging(&[2; 16]),
        "the conflict commits the deletion of its staging"
    );
    assert!(conflict.effects.is_empty());
}

#[test]
fn copied_hard_delete_attributes_are_written_with_their_new_objects() {
    let mut harness = Harness::new();
    harness.start_parent();
    let request = clone_request("hard-delete");
    let computed = harness.compute_parent_copy(&request);
    harness.stage_copy(&computed, &[1; 16]);
    let applied = harness.apply_clone_fork(FlowCloneForkCommand {
        copied_hard_delete_attributes: vec![AttributeWrite {
            key: "secret".to_owned(),
            value: Some(string_value("sealed")),
            is_hard_delete_on_null: true,
            ..AttributeWrite::default()
        }],
        hard_delete_objects: vec![HardDeleteObjectReference {
            write_index: 0,
            object_id: "object-of-the-clone".to_owned(),
            size_bytes: 8,
            is_string_value: true,
        }],
        ..harness.clone_fork_command(request, &[1; 16], CLONE_RUN)
    });
    assert!(
        matches!(
            applied.reply.reply,
            Some(command_reply::Reply::StartFlow(_))
        ),
        "{:?}",
        applied.reply
    );
    let clone_hash = harness.hasher.flow_hash(TenantId::DEFAULT, CLONE_FLOW);
    let secret = AttributeRecord::decode(
        harness
            .state
            .get(
                ColumnFamily::Flow,
                &run_named_key(clone_hash, RunId(CLONE_RUN), RunTable::Attribute, "secret"),
            )
            .expect("memory reads")
            .expect("the copied hard-delete Attribute exists")
            .as_slice(),
    )
    .expect("the Attribute decodes");
    assert!(secret.is_hard_delete_on_null);
    let Some(stored_value::Kind::HardDeleteObject(object)) =
        secret.value.and_then(|value| value.kind)
    else {
        panic!("the copied Attribute names its new object");
    };
    assert_eq!(object.object_id, "object-of-the-clone");
}

/// A CURRENT point over the memory budget is rejected after reading about one page of the state,
/// not the whole state.
#[test]
fn a_current_state_over_the_memory_budget_is_rejected_before_it_is_read_whole() {
    let mut harness = Harness::new();
    harness.start_parent();
    for batch in 0..20 {
        let request_id = format!("notes-{batch}");
        assert_ok(
            &harness.apply(
                "parent",
                &PARENT_RUN,
                Command::AttributesSet(AttributesSetCommand {
                    request: Some(SetAttributesRequest {
                        attributes: (0..100)
                            .map(|index| AttributeWrite {
                                key: format!("note-{batch:02}-{index:03}"),
                                value: Some(string_value("noted")),
                                ..AttributeWrite::default()
                            })
                            .collect(),
                        request_id: request_id.clone(),
                        ..SetAttributesRequest::default()
                    }),
                    request_fingerprint: request_id.into_bytes(),
                    ..AttributesSetCommand::default()
                }),
            ),
        );
    }
    let attribute_prefix = run_table_prefix(
        harness.hasher.flow_hash(TenantId::DEFAULT, "parent"),
        RunId(PARENT_RUN),
        RunTable::Attribute,
    );
    let (start, end) = prefix_range(&attribute_prefix);
    let attribute_bytes: usize = harness
        .state
        .scan(ColumnFamily::Flow, &start, &end, usize::MAX)
        .expect("memory reads")
        .iter()
        .map(|(key, value)| key.len() + value.len())
        .sum();
    let counting = ScanCountingState {
        state: &harness.state,
        scanned_bytes: std::cell::Cell::new(0),
    };
    let computation =
        harness.compute_parent_copy_over(&counting, &clone_request("over-budget"), 2 << 10);
    let CloneForkComputation::Rejected(reply) = computation else {
        panic!("the copy exceeds the memory budget");
    };
    let Some(command_reply::Reply::Error(error)) = &reply.reply else {
        panic!("the rejection is an error: {reply:?}");
    };
    assert_eq!(error.grpc_code, 8, "RESOURCE_EXHAUSTED");
    assert!(
        counting.scanned_bytes.get() < attribute_bytes / 4,
        "the computation read {} of {attribute_bytes} Attribute bytes",
        counting.scanned_bytes.get()
    );
}

#[test]
fn a_chunk_larger_than_one_command_carries_is_rejected() {
    let mut harness = Harness::new();
    let rows_before = harness.state.rows.clone();
    let rejected = harness.apply(
        CLONE_FLOW,
        &[],
        Command::FlowCloneForkStage(FlowCloneForkStageCommand {
            staging_id: vec![1; 16],
            chunk_index: 0,
            is_last_chunk: true,
            chunk: vec![0; 32 << 20],
        }),
    );
    assert_eq!(error_code(&rejected), Some(3), "INVALID_ARGUMENT");
    assert_eq!(harness.state.rows, rows_before);
}

impl Harness {
    /// Records a staging of another Flow on the clone's partition that holds `staged_bytes`.
    fn seed_partition_staging(&mut self, staged_bytes: u64) {
        let other_hash = self.hasher.flow_hash(TenantId::DEFAULT, "other-clone");
        let partition = self
            .hasher
            .partition_of(self.hasher.flow_hash(TenantId::DEFAULT, CLONE_FLOW));
        self.state.rows.insert(
            (
                ColumnFamily::PartitionIndex,
                dexzd_keyspace::row_keys::flow_clone_fork_staging_index_key(
                    partition, 1, other_hash, &[7; 16],
                ),
            ),
            FlowCloneForkStagingIndexRecord {
                tenant_id: 0,
                flow_id: "other-clone".to_owned(),
                staged_bytes,
            }
            .encode_to_vec(),
        );
    }
}

/// The partition's stagings hold at most 4 GiB of chunks; a replaced staging stops counting.
#[test]
fn a_partition_holds_at_most_4_gib_of_staged_chunks() {
    const MAX_PARTITION_STAGED_BYTES: u64 = 4 << 30;
    let mut harness = Harness::new();
    harness.start_parent();
    let first_chunk = harness
        .compute_parent_copy(&clone_request("bytes"))
        .stage_commands(&[1; 16])
        .expect("the copy fits")
        .remove(0);
    let first_chunk = FlowCloneForkStageCommand {
        is_last_chunk: false,
        ..first_chunk
    };
    let second_chunk = FlowCloneForkStageCommand {
        staging_id: vec![1; 16],
        chunk_index: 1,
        is_last_chunk: true,
        chunk: FlowCloneForkStagedChunk {
            values: vec![TimeTravelStagedValue {
                value_id: Some(StoredValueId {
                    run_seq: 1,
                    index_in_command: 0,
                }),
                value: Some(string_value("v")),
            }],
            ..FlowCloneForkStagedChunk::default()
        }
        .encode_to_vec(),
    };
    let first_bytes = first_chunk.chunk.len() as u64;
    let second_bytes = second_chunk.chunk.len() as u64;
    assert!(second_bytes < first_bytes);
    assert_ok(&harness.apply(
        CLONE_FLOW,
        &[],
        Command::FlowCloneForkStage(first_chunk.clone()),
    ));
    harness.seed_partition_staging(MAX_PARTITION_STAGED_BYTES - first_bytes - second_bytes + 1);
    let rows_before = harness.state.rows.clone();
    let rejected = harness.apply(
        CLONE_FLOW,
        &[],
        Command::FlowCloneForkStage(second_chunk.clone()),
    );
    assert_eq!(error_code(&rejected), Some(8), "RESOURCE_EXHAUSTED");
    assert_eq!(harness.state.rows, rows_before);
    let another_staging = harness.apply(
        CLONE_FLOW,
        &[],
        Command::FlowCloneForkStage(FlowCloneForkStageCommand {
            staging_id: vec![2; 16],
            ..first_chunk.clone()
        }),
    );
    assert_eq!(
        error_code(&another_staging),
        Some(8),
        "a new staging counts every staging in progress"
    );
    // A retry's chunk 0 replaces its staging, whose bytes stop counting.
    assert_ok(&harness.apply(CLONE_FLOW, &[], Command::FlowCloneForkStage(first_chunk)));
}

/// Two requests stage the same new Flow at once; the first FlowCloneFork creates the run, and
/// ID reuse decides the second, which deletes only its own staging.
#[test]
fn two_requests_racing_for_one_flow_are_decided_by_id_reuse() {
    let mut harness = Harness::new();
    harness.start_parent();
    let first = clone_request("racing-first");
    let second = CloneForkFlowRequest {
        id_reuse_policy: IdReusePolicy::DisallowReuse as i32,
        ..clone_request("racing-second")
    };
    let first_copy = harness.compute_parent_copy(&first);
    let second_copy = harness.compute_parent_copy(&second);
    harness.stage_copy(&first_copy, &[1; 16]);
    harness.stage_copy(&second_copy, &[2; 16]);
    let created = harness.apply_clone_fork(harness.clone_fork_command(first, &[1; 16], CLONE_RUN));
    assert!(matches!(
        created.reply.reply,
        Some(command_reply::Reply::StartFlow(_))
    ));
    assert!(
        harness.has_clone_staging(&[2; 16]),
        "the other request's staging stays"
    );
    let conflict = harness.apply_clone_fork(harness.clone_fork_command(second, &[2; 16], [8; 16]));
    assert_eq!(error_code(&conflict), Some(6), "ALREADY_EXISTS");
    assert!(!harness.has_clone_staging(&[2; 16]));
    let clone_hash = harness.hasher.flow_hash(TenantId::DEFAULT, CLONE_FLOW);
    let directory = FlowDirectoryRecord::decode(
        harness
            .state
            .get(
                ColumnFamily::Flow,
                &flow_directory_key(clone_hash, TenantId::DEFAULT, CLONE_FLOW),
            )
            .expect("memory reads")
            .expect("the Flow exists")
            .as_slice(),
    )
    .expect("the directory decodes");
    assert_eq!(directory.runs.len(), 1);
    assert_eq!(directory.runs[0].run_id, CLONE_RUN.to_vec());
}

/// An expiry that commits first deletes the staging, so FlowCloneFork is ABORTED; one that
/// commits after FlowCloneFork finds nothing to delete.
#[test]
fn an_expiry_racing_the_final_command_leaves_one_outcome() {
    let mut harness = Harness::new();
    harness.start_parent();
    let request = clone_request("expiring");
    let computed = harness.compute_parent_copy(&request);
    harness.stage_copy(&computed, &[1; 16]);
    harness.now_micros += 10 * 60 * SECOND;
    let expire = || {
        Command::FlowCloneForkStagingExpire(FlowCloneForkStagingExpireCommand {
            staging_id: vec![1; 16],
        })
    };
    assert!(!harness.apply(CLONE_FLOW, &[], expire()).writes.is_empty());
    let rows_before = harness.state.rows.clone();
    let aborted =
        harness.apply_clone_fork(harness.clone_fork_command(request.clone(), &[1; 16], CLONE_RUN));
    assert_eq!(error_code(&aborted), Some(10), "ABORTED");
    assert_eq!(harness.state.rows, rows_before, "no run is created");

    harness.stage_copy(&computed, &[1; 16]);
    let created =
        harness.apply_clone_fork(harness.clone_fork_command(request, &[1; 16], CLONE_RUN));
    assert!(matches!(
        created.reply.reply,
        Some(command_reply::Reply::StartFlow(_))
    ));
    harness.now_micros += 10 * 60 * SECOND;
    let late = harness.apply(CLONE_FLOW, &[], expire());
    assert!(late.writes.is_empty(), "the run consumed the staging");
}

fn string_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}
