// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_apply::FlowStateReader;
use dexzd_keyspace::RunId;
use dexzd_protocol::dex::{PullRouting, StepDurability};
use dexzd_protocol::dexzd::v1::{
    CommandReply, FlowCommand, StepAttemptStartedCommand, StepInvocationKey, StepInvocationPhase,
    command_reply, flow_command,
};
use futures::future::BoxFuture;

use crate::effects::{
    EffectError, FlowEffectOperationError, FlowEffectOperations, InvocationStartOutcome,
    RunLocation, read_run_record, read_step_execution, resolve_method_durability,
};
use crate::flow_coordination::{
    CommitUrgency, FlowAddress, FlowCoordinationError, FlowCoordinatorRegistry, PendingInvocation,
};

impl FlowEffectOperations for FlowCoordinatorRegistry {
    fn propose_committed<'a>(
        &'a self,
        flow: &'a FlowAddress,
        command: FlowCommand,
        urgency: CommitUrgency,
    ) -> BoxFuture<'a, Result<CommandReply, FlowEffectOperationError>> {
        Box::pin(async move {
            let proposal = self
                .propose(flow.clone(), command, urgency)
                .await
                .map_err(flow_effect_operation_error)?;
            let committed = proposal.ticket.wait().await?;
            Ok(committed.outcome.reply)
        })
    }

    fn propose_speculative<'a>(
        &'a self,
        flow: &'a FlowAddress,
        command: FlowCommand,
        urgency: CommitUrgency,
    ) -> BoxFuture<'a, Result<CommandReply, FlowEffectOperationError>> {
        Box::pin(async move {
            let proposal = self
                .propose(flow.clone(), command, urgency)
                .await
                .map_err(flow_effect_operation_error)?;
            Ok(proposal.speculative.reply)
        })
    }

    fn sync_barrier<'a>(
        &'a self,
        flow: &'a FlowAddress,
    ) -> BoxFuture<'a, Result<(), FlowEffectOperationError>> {
        Box::pin(async move {
            FlowCoordinatorRegistry::sync_barrier(self, flow)
                .await
                .map_err(flow_effect_operation_error)
        })
    }

    fn wait_for_transactional_rpcs<'a>(
        &'a self,
        flow: &'a FlowAddress,
    ) -> BoxFuture<'a, Result<(), FlowEffectOperationError>> {
        Box::pin(async move {
            FlowCoordinatorRegistry::wait_for_transactional_rpcs(self, flow)
                .await
                .map_err(flow_effect_operation_error)
        })
    }

    fn try_start_invocation<'a>(
        &'a self,
        flow: &'a FlowAddress,
        run_id: RunId,
        run_attempt: u32,
        invocation: &'a StepInvocationKey,
        attempt: u32,
        task_id: Vec<u8>,
        pull_routing: PullRouting,
    ) -> BoxFuture<'a, Result<InvocationStartOutcome, FlowEffectOperationError>> {
        Box::pin(async move {
            let run = RunLocation {
                flow_hash: self.flow_context(flow).flow_hash,
                run_id,
            };
            let reader = self
                .flow_state_reader(flow, true)
                .map_err(flow_effect_operation_error)?;
            let Some(start) = read_pending_start(reader.as_ref(), run, invocation, attempt)
                .map_err(read_error)?
            else {
                return Ok(InvocationStartOutcome::Stale);
            };
            let command = FlowCommand {
                flow_id: flow.flow_id.clone(),
                run_id: run_id.as_bytes().to_vec(),
                expected_run_attempt: run_attempt,
                tenant_id: flow.tenant_id.0,
                command: Some(flow_command::Command::StepAttemptStarted(
                    StepAttemptStartedCommand {
                        invocation: Some(invocation.clone()),
                        attempt,
                        task_id,
                        pull_routing: Some(pull_routing),
                    },
                )),
                ..FlowCommand::default()
            };
            let pending = PendingInvocation {
                run_id,
                invocation: invocation.clone(),
                lock_keys: start.lock_keys,
                pending_since_seq: start.pending_since_seq,
            };
            let proposal = self
                .propose_invocation_start(flow, &pending, command, start.urgency)
                .await
                .map_err(flow_effect_operation_error)?;
            Ok(match proposal {
                None => InvocationStartOutcome::LocksBusy,
                Some(result)
                    if matches!(
                        result.speculative.reply.reply,
                        Some(command_reply::Reply::Error(_))
                    ) =>
                {
                    InvocationStartOutcome::Stale
                }
                Some(_) => InvocationStartOutcome::Started,
            })
        })
    }

    fn flow_state_reader(
        &self,
        flow: &FlowAddress,
        should_include_speculative: bool,
    ) -> Result<Box<dyn FlowStateReader + Send>, FlowEffectOperationError> {
        FlowCoordinatorRegistry::flow_state_reader(self, flow, should_include_speculative)
            .map_err(flow_effect_operation_error)
    }

    fn committed_state_reader(
        &self,
    ) -> Result<Box<dyn FlowStateReader + Send>, FlowEffectOperationError> {
        self.check_running().map_err(flow_effect_operation_error)?;
        Ok(self.inner.dependencies.committer.committed_view()?)
    }
}

/// What starting a still-pending attempt needs from its Step row.
struct PendingStart {
    lock_keys: Vec<String>,
    pending_since_seq: u64,
    urgency: CommitUrgency,
}

/// Returns the attempt's lock keys, queue position, and urgency, or `None` when it is no longer
/// pending with `attempt`.
fn read_pending_start(
    reader: &dyn FlowStateReader,
    run: RunLocation,
    invocation: &StepInvocationKey,
    attempt: u32,
) -> Result<Option<PendingStart>, EffectError> {
    let Some(step_execution) = read_step_execution(reader, run, &invocation.step_execution_id)?
    else {
        return Ok(None);
    };
    let Some(record) = step_execution.invocation.as_ref() else {
        return Ok(None);
    };
    let is_pending = record.phase == StepInvocationPhase::Pending as i32
        && record.attempt == attempt
        && record.method == invocation.method;
    if !is_pending {
        return Ok(None);
    }
    let Some(run_record) = read_run_record(reader, run)? else {
        return Ok(None);
    };
    let urgency = match resolve_method_durability(&run_record, &step_execution, invocation.method) {
        StepDurability::Async => CommitUrgency::Batched,
        _ => CommitUrgency::Prompt,
    };
    Ok(Some(PendingStart {
        lock_keys: record.lock_keys.clone(),
        pending_since_seq: record.pending_since_seq,
        urgency,
    }))
}

fn flow_effect_operation_error(error: FlowCoordinationError) -> FlowEffectOperationError {
    match error {
        FlowCoordinationError::NodeStopping { detail } => {
            FlowEffectOperationError::NodeStopping { detail }
        }
        FlowCoordinationError::Unavailable { detail } => {
            FlowEffectOperationError::Unavailable { detail }
        }
        // Effect proposals are built by the server; any other rejection is a broken invariant.
        other => FlowEffectOperationError::NodeStopping {
            detail: other.to_string(),
        },
    }
}

fn read_error(error: EffectError) -> FlowEffectOperationError {
    FlowEffectOperationError::Unavailable {
        detail: error.to_string(),
    }
}
