// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Reviving offloaded values that a proposed command references (plan section 5.11).
//!
//! The check runs inside the proposal's critical section on the speculative state, which is
//! exactly the state the command applies on, so no command can offload a value in between.
//! Values are read from object storage outside it, holding no view.

use dexzd_apply::read_model::{OffloadedReference, ReadModelError, offloaded_command_references};
use dexzd_keyspace::RunId;
use dexzd_protocol::dexzd::v1::{FlowCommand, ValueReviveCommand, command_reply, flow_command};

use crate::flow_coordination::registry::{ProposalResult, ProposalScope};
use crate::flow_coordination::{
    CommitUrgency, FlowAddress, FlowCoordinationError, FlowCoordinatorRegistry,
};

/// Rounds of reviving before a command is proposed without the check. A revived value stays
/// local for its grace period, so a second round only meets values offloaded meanwhile.
const MAX_VALUE_REVIVAL_ROUNDS: usize = 3;

/// The run and offloaded values a built command references.
struct ReviveRequest {
    command: FlowCommand,
    offloaded: Vec<OffloadedReference>,
}

impl FlowCoordinatorRegistry {
    /// Like [`Self::propose_once_with_admission`], but first revives every offloaded value of
    /// the command's run that the built command references, so it commits unchanged.
    ///
    /// When a revive is rejected, such as for a purged run, or values keep leaving, the command
    /// is proposed as built, and apply replies as it does to any unknown reference.
    pub(super) async fn propose_with_admission<T>(
        &self,
        flow: &FlowAddress,
        urgency: CommitUrgency,
        should_wait_for_capacity: bool,
        mut build: impl FnMut(
            &mut ProposalScope<'_>,
        ) -> Result<Option<(FlowCommand, T)>, FlowCoordinationError>,
    ) -> Result<Option<(ProposalResult, T)>, FlowCoordinationError> {
        let mut should_check = true;
        let mut round = 0;
        loop {
            should_check &= round < MAX_VALUE_REVIVAL_ROUNDS;
            round += 1;
            let mut revive_request = None;
            let proposal = self
                .propose_once_with_admission(flow, urgency, should_wait_for_capacity, |scope| {
                    revive_request = None;
                    let Some((command, extra)) = build(scope)? else {
                        return Ok(None);
                    };
                    if should_check {
                        let offloaded = offloaded_command_references(
                            scope.state,
                            scope.context.flow_hash,
                            &command,
                        )
                        .map_err(|error| offloaded_reference_error(flow, error))?;
                        if !offloaded.is_empty() {
                            revive_request = Some(ReviveRequest { command, offloaded });
                            return Ok(None);
                        }
                    }
                    Ok(Some((command, extra)))
                })
                .await?;
            let Some(request) = revive_request else {
                return Ok(proposal);
            };
            should_check = self.revive_offloaded_values(flow, urgency, request).await?;
        }
    }

    /// Reads the request's values and proposes one ValueRevive command for each.
    ///
    /// Returns false when apply rejects a revive, so the caller stops reviving.
    async fn revive_offloaded_values(
        &self,
        flow: &FlowAddress,
        urgency: CommitUrgency,
        request: ReviveRequest,
    ) -> Result<bool, FlowCoordinationError> {
        let run_id = RunId::from_slice(&request.command.run_id)
            .expect("only a command with a run ID references offloaded values");
        let values = self
            .read_offloaded_values(flow, run_id, &request.offloaded)
            .await?;
        for (value_id, value) in values {
            let revive = FlowCommand {
                flow_id: request.command.flow_id.clone(),
                run_id: request.command.run_id.clone(),
                tenant_id: request.command.tenant_id,
                command: Some(flow_command::Command::ValueRevive(ValueReviveCommand {
                    value_run_seq: value_id.run_seq,
                    value_index_in_command: value_id.index_in_command,
                    value: Some(value),
                })),
                ..FlowCommand::default()
            };
            let proposal = self
                .propose_once_with_admission(flow, urgency, true, |_| {
                    Ok(Some((revive.clone(), ())))
                })
                .await?;
            let (result, ()) = proposal.expect("the revive builder always proposes");
            if let Some(command_reply::Reply::Error(error)) = &result.speculative.reply.reply {
                tracing::warn!(
                    flow_id = %flow.flow_id,
                    %run_id,
                    value_run_seq = value_id.run_seq,
                    value_index_in_command = value_id.index_in_command,
                    grpc_code = error.grpc_code,
                    detail = %error.message,
                    "an offloaded value revive was rejected"
                );
                return Ok(false);
            }
            self.inner
                .dependencies
                .history_segment_reader
                .record_value_revival();
        }
        Ok(true)
    }
}

fn offloaded_reference_error(flow: &FlowAddress, error: ReadModelError) -> FlowCoordinationError {
    match error {
        ReadModelError::State(error) => error.into(),
        other => FlowCoordinationError::Internal {
            detail: format!(
                "checking offloaded value references of Flow {}: {other}",
                flow.flow_id
            ),
        },
    }
}
