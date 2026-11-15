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
use dexzd_protocol::dexzd::v1::FlowCloneForkCommand;

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

impl Harness {
    /// Computes the copy of the parent's current point as an owner does.
    fn compute_parent_copy(&self, request: &CloneForkFlowRequest) -> ComputedCloneFork {
        let parent_hash = self.hasher.flow_hash(TenantId::DEFAULT, "parent");
        let context = ApplyContext {
            partition: self.hasher.partition_of(parent_hash),
            flow_hash: parent_hash,
        };
        let history = dexzd_apply::StateHistorySource::new(&self.state, context, RunId(PARENT_RUN));
        let child_flow_runs = dexzd_apply::StateChildFlowRuns::new(&self.state, &self.hasher);
        let computation = dexzd_apply::compute_clone_fork(
            &context,
            &dexzd_apply::TimeTravelComputationSources {
                state: &self.state,
                history: &history,
                offloaded_values: &dexzd_apply::values::NoOffloadedValues,
                child_flow_runs: &child_flow_runs,
            },
            &CloneForkComputationRequest {
                source_run_id: RunId(PARENT_RUN),
                clone_fork: request,
                now_micros: self.now_micros,
                memory_budget_bytes: u64::MAX,
            },
        )
        .expect("the clone fork computes");
        match computation {
            CloneForkComputation::Computed(computed) => *computed,
            CloneForkComputation::Rejected(reply) => panic!("the copy is rejected: {reply:?}"),
        }
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

fn string_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}
