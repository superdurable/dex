// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! InvokeRPC: read-only, non-transactional, and transactional paths (plan 9.4).

use std::collections::{BTreeMap, BTreeSet};
use std::future::Future;
use std::time::Duration;

use dexzd_apply::{ApplyContext, FlowStateReader};
use dexzd_keyspace::row_keys::{RequestKind, channel_name_prefix, request_dedup_key};
use dexzd_keyspace::{ColumnFamily, RunId, RunTable, TenantId};
use dexzd_protocol::dex::{
    ChannelInfo, ChannelMessage, ChannelValues, Context, ErrorSubStatus, FlowStatus,
    InvokeRpcRequest, InvokeRpcResponse, InvokeWorkerRpcRequest, InvokeWorkerRpcResponse, Kv,
};
use dexzd_protocol::dexzd::v1::{
    AttributeRecord, ChannelMessageRecord, ChannelMetaRecord, RequestDedupRecord,
    RpcCommittedCommand, command_reply, flow_command,
};
use dexzd_request_validation::{
    ClientRequest, WorkerResponse, validate_client_request, validate_worker_response,
};
use prost::Message;

use crate::flow_coordination::client_operations::hard_delete_uploads::hard_delete_references;
use crate::flow_coordination::client_operations::request_fingerprint::request_fingerprint;
use crate::flow_coordination::client_operations::rpc_requests::RpcRequestGuard;
use crate::flow_coordination::client_operations::unstamped_flow_command;
use crate::flow_coordination::read_operations::state_rows::{
    ResolvedRun, WireValue, decode_row, hydrate_value_reference, load_complete_value, read_run,
    response_value, scan_run_rows,
};
use crate::flow_coordination::registry::TransactionalRpcAdmission;
use crate::flow_coordination::{
    FlowAddress, FlowCoordinationError, FlowCoordinatorRegistry, RpcInvokeError,
    WorkerRpcInvocation, WorkerRpcResult, grpc_code,
};

// Commit outcomes may be unknown at this bound; it is independent of the Worker's RPC timeout.
const RPC_COMMIT_PROCESSING_BUDGET: Duration = Duration::from_secs(60);

/// An RPC snapshot whose hard-delete Attribute values are still to be read.
struct RpcSnapshot {
    run: ResolvedRun,
    worker_request: InvokeWorkerRpcRequest,
    hard_delete_attributes: Vec<(usize, dexzd_protocol::dexzd::v1::HardDeleteObjectReference)>,
}

impl FlowCoordinatorRegistry {
    /// Invokes an RPC handler on a Worker against a committed snapshot and commits its effects.
    ///
    /// A read-only RPC (no locks, not transactional, no effects) is never logged. Lock keys are
    /// taken for the whole call; any held key fails with `ABORTED`. A transactional RPC fails
    /// with `FAILED_PRECONDITION` while a natural close waits for the Attribute Sync outbox.
    ///
    /// A transactional or locking RPC replays its recorded request ID, else fails `NOT_FOUND` on a closed run.
    pub async fn invoke_rpc(
        &self,
        tenant_id: TenantId,
        request: InvokeRpcRequest,
    ) -> Result<InvokeRpcResponse, FlowCoordinationError> {
        validate_client_request(
            ClientRequest::InvokeRpc(&request),
            &self.inner.config.validation,
        )?;
        self.check_running()?;
        let flow = FlowAddress::new(tenant_id, request.flow_id.clone());
        let is_transactional = request.is_transactional || !request.lock_attribute_keys.is_empty();
        let run_id = {
            let committed = self.inner.dependencies.committer.committed_view()?;
            let run = read_run(
                committed.as_ref(),
                self.flow_context(&flow),
                &flow,
                &request.run_id,
            )?;
            if is_transactional
                && let Some(response) = replay_recorded_rpc(
                    committed.as_ref(),
                    self.flow_context(&flow),
                    run.run_id,
                    &request,
                )?
            {
                return Ok(response);
            }
            run.run_id
        };
        if !is_transactional {
            return self
                .invoke_rpc_once(flow, run_id, request, is_transactional, None)
                .await;
        }
        let deadline = self.rpc_deadline(&request);
        let admission = self.inner.rpc_requests.join_or_insert(
            &flow,
            run_id,
            &request.request_id,
            request_fingerprint(&request),
        );
        let (mut result, guard) = match admission {
            Ok(admission) => admission,
            Err(error) => {
                // A commit can land between the snapshot and a conflicting in-flight admission.
                let committed = self.inner.dependencies.committer.committed_view()?;
                if let Some(response) = replay_recorded_rpc(
                    committed.as_ref(),
                    self.flow_context(&flow),
                    run_id,
                    &request,
                )? {
                    return Ok(response);
                }
                return Err(error);
            }
        };
        if let Some(guard) = guard {
            let task = tokio::spawn(
                self.clone()
                    .run_shared_rpc(flow, run_id, request, deadline, guard),
            );
            // The registry tracks this task; individual callers own only their result subscriptions.
            drop(task);
        }
        result
            .wait_for(Option::is_some)
            .await
            .map_err(|_| FlowCoordinationError::NodeStopping {
                detail: "RPC task ended without a result".to_owned(),
            })?
            .as_ref()
            .expect("wait_for observed a result")
            .clone()
    }

    async fn run_shared_rpc(
        self,
        flow: FlowAddress,
        run_id: RunId,
        request: InvokeRpcRequest,
        deadline: tokio::time::Instant,
        guard: RpcRequestGuard,
    ) {
        let halted = self.halted();
        let operation = self.invoke_rpc_once(flow, run_id, request, true, Some(deadline));
        let result = tokio::select! {
            biased;
            () = self.inner.rpc_requests.stopped.cancelled() => Err(FlowCoordinationError::NodeStopping {
                detail: "node stopped while an RPC was in flight".to_owned(),
            }),
            result = operation => result,
            () = halted.cancelled() => Err(self.check_running().expect_err("halted")),
        };
        guard.finish(result);
    }

    async fn invoke_rpc_once(
        &self,
        flow: FlowAddress,
        run_id: RunId,
        request: InvokeRpcRequest,
        is_transactional: bool,
        deadline: Option<tokio::time::Instant>,
    ) -> Result<InvokeRpcResponse, FlowCoordinationError> {
        self.check_running()?;
        let context = self.flow_context(&flow);
        let lease = self.lease(&flow);
        let mut lock_keys = request.lock_attribute_keys.clone();
        lock_keys.sort();
        lock_keys.dedup();
        if is_transactional {
            let committed = self.inner.dependencies.committer.committed_view()?;
            let run = read_run(committed.as_ref(), context, &flow, &run_id.to_string())?;
            if let Some(response) =
                admit_transactional_rpc_run(committed.as_ref(), context, &flow, &run, &request)?
            {
                return Ok(response);
            }
        }
        // The Go Server rejects these workflow updates during cleanup, and waits for admitted ones.
        let rpc_admission = if is_transactional {
            let admission = self.admit_transactional_rpc(&flow, run_id, |committed| {
                replay_recorded_rpc(committed, context, run_id, &request)?
                    .ok_or_else(|| FlowCoordinationError::flow_not_found(&flow.flow_id))
            })?;
            match admission {
                TransactionalRpcAdmission::Admitted(guard) => Some(guard),
                TransactionalRpcAdmission::Replayed(response) => return Ok(response),
            }
        } else {
            None
        };
        let lock_guard = if lock_keys.is_empty() {
            None
        } else {
            Some(self.acquire_rpc_locks(lease, run_id, &lock_keys)?)
        };
        if lock_guard.is_some() {
            // Writes proposed before the locks were taken must be in the snapshot (plan 8.3, L2).
            wait_for_rpc_before_commit(deadline, self.sync_barrier(&flow)).await?;
        }
        let is_hydrating = self.inner.config.hydrate_value_references;
        let snapshot = {
            let committed = self.inner.dependencies.committer.committed_view()?;
            if lock_guard.is_some() {
                // A StopFlow may commit while the RPC waits for its locks and the sync barrier.
                let run = read_run(committed.as_ref(), context, &flow, &run_id.to_string())?;
                if let Some(response) =
                    admit_transactional_rpc_run(committed.as_ref(), context, &flow, &run, &request)?
                {
                    return Ok(response);
                }
            }
            build_rpc_snapshot(
                committed.as_ref(),
                context,
                &flow,
                &run_id.to_string(),
                &request,
                is_hydrating,
            )?
        };
        let run = snapshot.run.clone();
        let worker_request =
            wait_for_rpc_before_commit(deadline, self.complete_rpc_snapshot(&flow, snapshot))
                .await?;
        if worker_request.encoded_len() > self.inner.config.max_worker_rpc_request_bytes {
            return Err(FlowCoordinationError::rejected(
                grpc_code::RESOURCE_EXHAUSTED,
                ErrorSubStatus::Uncategorized,
                format!(
                    "RPC Worker request exceeds the {}-byte gRPC message limit",
                    self.inner.config.max_worker_rpc_request_bytes
                ),
            ));
        }
        let is_shared = deadline.is_some();
        let deadline = deadline.unwrap_or_else(|| self.rpc_deadline(&request));
        if tokio::time::Instant::now() >= deadline {
            return Err(rpc_invoke_error(RpcInvokeError::DeadlineExceeded));
        }
        let call = self
            .inner
            .dependencies
            .rpc_invoker
            .invoke_worker_rpc(WorkerRpcInvocation {
                flow: flow.clone(),
                run_id: run.run_id,
                flow_type: run.record.flow_type.clone(),
                sticky_app_worker_id: run.record.sticky_app_worker_id.clone(),
                request: worker_request,
                deadline,
            });
        // A stalled invoker must not hold the RPC locks past the deadline (plan 8.3).
        let result = tokio::time::timeout_at(deadline, call)
            .await
            .unwrap_or(Err(RpcInvokeError::DeadlineExceeded))
            .map_err(rpc_invoke_error)?;
        if tokio::time::Instant::now() >= deadline {
            return Err(rpc_invoke_error(RpcInvokeError::DeadlineExceeded));
        }
        validate_worker_response(
            WorkerResponse::Rpc(Some(&result.response)),
            &self.inner.config.validation,
        )
        .map_err(|error| {
            FlowCoordinationError::rejected(
                grpc_code::INTERNAL,
                ErrorSubStatus::Uncategorized,
                error.message,
            )
        })?;
        if !is_transactional && !has_rpc_effects(&result.response) {
            let mut output = result.response.output;
            if is_hydrating && let Some(output) = output.as_mut() {
                let committed = self.inner.dependencies.committer.committed_view()?;
                if !hydrate_value_reference(committed.as_ref(), context.flow_hash, run_id, output)?
                {
                    return Err(FlowCoordinationError::rejected(
                        grpc_code::FAILED_PRECONDITION,
                        ErrorSubStatus::WorkerApiError,
                        "worker RPC output is a value reference that names no value of this run",
                    ));
                }
            }
            return Ok(InvokeRpcResponse { output });
        }
        let commit = self.commit_rpc(&flow, &run, request, result, is_transactional);
        let output = if is_shared {
            tokio::time::timeout(RPC_COMMIT_PROCESSING_BUDGET, commit)
                .await
                .unwrap_or_else(|_| {
                    Err(FlowCoordinationError::Unavailable {
                        detail: "RPC commit processing exceeded its server budget; the outcome is unknown; retry with the same request ID".to_owned(),
                    })
                })
        } else {
            commit.await
        };
        drop(lock_guard);
        drop(rpc_admission);
        output
    }

    fn rpc_deadline(&self, request: &InvokeRpcRequest) -> tokio::time::Instant {
        let timeout = if request.timeout_seconds > 0 {
            std::time::Duration::from_secs(u64::from(request.timeout_seconds.unsigned_abs()))
        } else {
            self.inner.config.rpc_default_timeout
        };
        tokio::time::Instant::now() + timeout
    }

    async fn complete_rpc_snapshot(
        &self,
        flow: &FlowAddress,
        snapshot: RpcSnapshot,
    ) -> Result<InvokeWorkerRpcRequest, FlowCoordinationError> {
        let RpcSnapshot {
            run,
            mut worker_request,
            hard_delete_attributes,
        } = snapshot;
        for (index, object) in hard_delete_attributes {
            let value = self
                .read_hard_delete_attribute(
                    flow,
                    run.run_id,
                    &worker_request.attributes[index].key,
                    object,
                )
                .await?;
            worker_request.attributes[index].value = value;
        }
        worker_request
            .attributes
            .retain(|attribute| attribute.value.is_some());
        Ok(worker_request)
    }

    async fn commit_rpc(
        &self,
        flow: &FlowAddress,
        run: &ResolvedRun,
        request: InvokeRpcRequest,
        result: WorkerRpcResult,
        is_transactional: bool,
    ) -> Result<InvokeRpcResponse, FlowCoordinationError> {
        let fingerprint = request_fingerprint(&request);
        let mut response = result.response;
        // Apply cannot mint IDs, so every publication gets a new one, as Go's AssignIDs does.
        for message in &mut response.publish_to_channel {
            message.message_id = self
                .inner
                .dependencies
                .id_source
                .channel_message_id()
                .to_string();
        }
        let uploads = self
            .upload_hard_delete_values(flow, run.run_id, &mut response.upsert_attributes)
            .await?;
        let mut command = unstamped_flow_command(
            flow,
            Some(run.run_id),
            flow_command::Command::RpcCommitted(RpcCommittedCommand {
                request: Some(request),
                response: Some(response),
                app_worker_id: result.app_worker_id,
                hard_delete_objects: hard_delete_references(uploads.as_ref()),
                request_fingerprint: fingerprint,
                pull_routing: result.pull_routing,
                is_transactional,
            }),
        );
        if is_transactional {
            // Apply rejects the commit when time travel replaced the attempt the snapshot read.
            command.expected_run_attempt = run.record.current_attempt;
        }
        let reply = self
            .propose_client_command_with_uploads(flow, uploads, |scope| {
                scope.is_admitted_rpc_commit = is_transactional;
                Ok(command.clone())
            })
            .await?;
        let command_reply::Reply::Rpc(rpc_reply) = reply else {
            return Err(FlowCoordinationError::Internal {
                detail: format!("apply returned an unexpected reply for an RPC: {reply:?}"),
            });
        };
        let Some(stored_output) = rpc_reply.output else {
            return Ok(InvokeRpcResponse { output: None });
        };
        let context = self.flow_context(flow);
        let committed = self.inner.dependencies.committer.committed_view()?;
        let output = load_complete_value(
            committed.as_ref(),
            context.flow_hash,
            run.run_id,
            &stored_output,
        )?;
        Ok(InvokeRpcResponse { output })
    }
}

async fn wait_for_rpc_before_commit<T>(
    deadline: Option<tokio::time::Instant>,
    operation: impl Future<Output = Result<T, FlowCoordinationError>>,
) -> Result<T, FlowCoordinationError> {
    match deadline {
        Some(deadline) => tokio::time::timeout_at(deadline, operation)
            .await
            .unwrap_or_else(|_| Err(rpc_invoke_error(RpcInvokeError::DeadlineExceeded))),
        None => operation.await,
    }
}

fn admit_transactional_rpc_run(
    state: &dyn FlowStateReader,
    context: ApplyContext,
    flow: &FlowAddress,
    run: &ResolvedRun,
    request: &InvokeRpcRequest,
) -> Result<Option<InvokeRpcResponse>, FlowCoordinationError> {
    if let Some(response) = replay_recorded_rpc(state, context, run.run_id, request)? {
        return Ok(Some(response));
    }
    if run.record.status != FlowStatus::Running as i32 {
        return Err(FlowCoordinationError::flow_not_found(&flow.flow_id));
    }
    Ok(None)
}

fn replay_recorded_rpc(
    state: &dyn FlowStateReader,
    context: ApplyContext,
    run_id: RunId,
    request: &InvokeRpcRequest,
) -> Result<Option<InvokeRpcResponse>, FlowCoordinationError> {
    let key = request_dedup_key(
        context.flow_hash,
        run_id,
        RequestKind::InvokeRpc,
        &request.request_id,
    );
    let recorded: Option<RequestDedupRecord> =
        decode_row(state, ColumnFamily::Flow, &key, "RPC request dedup")?;
    let Some(recorded) = recorded else {
        return Ok(None);
    };
    if recorded.request_fingerprint != request_fingerprint(request) {
        return Err(FlowCoordinationError::invalid_argument(format!(
            "request ID {} was already used for a different request",
            request.request_id
        )));
    }
    match recorded.reply.and_then(|reply| reply.reply) {
        Some(command_reply::Reply::Rpc(reply)) => {
            let output = match reply.output {
                Some(stored) => load_complete_value(state, context.flow_hash, run_id, &stored)?,
                None => None,
            };
            Ok(Some(InvokeRpcResponse { output }))
        }
        Some(command_reply::Reply::Error(error)) => Err(FlowCoordinationError::from_reply(&error)),
        other => Err(FlowCoordinationError::Internal {
            detail: format!("RPC dedup entry has an unexpected reply: {other:?}"),
        }),
    }
}

/// Reads the run's Attributes and Channels into the Worker request, with complete values when
/// `is_hydrating`.
///
/// Returns `INVALID_ARGUMENT` when hydrating and the RPC input is a value reference that names no
/// value of the run.
fn build_rpc_snapshot(
    state: &dyn FlowStateReader,
    context: ApplyContext,
    flow: &FlowAddress,
    run_id: &str,
    request: &InvokeRpcRequest,
    is_hydrating: bool,
) -> Result<RpcSnapshot, FlowCoordinationError> {
    let run = read_run(state, context, flow, run_id)?;
    let run_id = run.run_id;
    let mut input = request.input.clone();
    if is_hydrating
        && let Some(input) = input.as_mut()
        && !hydrate_value_reference(state, context.flow_hash, run_id, input)?
    {
        return Err(FlowCoordinationError::invalid_argument(
            "RPC input is a value reference that names no value of this run",
        ));
    }
    let attribute_selections = sorted_unique(&request.load_attribute_map_instances);
    let channel_names = sorted_unique(&request.load_channel_names);
    let channel_selections = sorted_unique(&request.load_channel_map_instances);

    let mut attributes = Vec::new();
    let mut hard_delete_attributes = Vec::new();
    let rows: Vec<(Vec<u8>, AttributeRecord)> =
        scan_run_rows(state, context.flow_hash, run_id, RunTable::Attribute, &[])?;
    for (suffix, record) in rows {
        let key = String::from_utf8(suffix)
            .map_err(|error| FlowCoordinationError::corrupt_row("Attribute key", error))?;
        if !is_selected(&key, &attribute_selections) {
            continue;
        }
        let Some(stored) = record.value.as_ref() else {
            continue;
        };
        let Some(value) = response_value(state, context.flow_hash, run_id, stored, is_hydrating)?
        else {
            continue;
        };
        match value {
            WireValue::Value(value) => attributes.push(Kv {
                key,
                value: Some(value),
            }),
            WireValue::HardDeleteObject(object) => {
                hard_delete_attributes.push((attributes.len(), object));
                attributes.push(Kv { key, value: None });
            }
        }
    }

    let mut channel_infos = BTreeMap::new();
    let mut loaded_channel_messages = BTreeMap::new();
    let metas: Vec<(Vec<u8>, ChannelMetaRecord)> =
        scan_run_rows(state, context.flow_hash, run_id, RunTable::ChannelMeta, &[])?;
    let mut nonempty_channels = BTreeSet::new();
    for (suffix, meta) in metas {
        let channel_name = String::from_utf8(suffix)
            .map_err(|error| FlowCoordinationError::corrupt_row("Channel name", error))?;
        if meta.pending_count == 0 {
            continue;
        }
        channel_infos.insert(
            channel_name.clone(),
            ChannelInfo {
                size: i32::try_from(meta.pending_count).unwrap_or(i32::MAX),
            },
        );
        nonempty_channels.insert(channel_name);
    }
    let mut loaded_channels: BTreeSet<String> = channel_names.iter().cloned().collect();
    for selection in &channel_selections {
        if selection.ends_with('/') {
            loaded_channels.extend(
                nonempty_channels
                    .iter()
                    .filter(|name| name.starts_with(selection.as_str()))
                    .cloned(),
            );
        } else {
            loaded_channels.insert(selection.clone());
        }
    }
    for channel_name in loaded_channels {
        let messages = read_channel_messages(state, context, run_id, &channel_name, is_hydrating)?;
        loaded_channel_messages.insert(channel_name, ChannelValues { messages });
    }

    let worker_request = InvokeWorkerRpcRequest {
        context: Some(Context {
            flow_id: flow.flow_id.clone(),
            run_id: run_id.to_string(),
            flow_started_timestamp: run.record.started_at_micros.div_euclid(1_000_000),
            ..Context::default()
        }),
        flow_type: run.record.flow_type.clone(),
        rpc_name: request.rpc_name.clone(),
        input,
        attributes,
        channel_infos,
        loaded_channel_messages,
        loaded_attribute_map_instances: attribute_selections,
        loaded_channel_names: channel_names,
        loaded_channel_map_instances: channel_selections,
    };
    Ok(RpcSnapshot {
        run,
        worker_request,
        hard_delete_attributes,
    })
}

fn read_channel_messages(
    state: &dyn FlowStateReader,
    context: ApplyContext,
    run_id: RunId,
    channel_name: &str,
    is_hydrating: bool,
) -> Result<Vec<ChannelMessage>, FlowCoordinationError> {
    let suffix_prefix = channel_name_prefix(channel_name);
    let rows: Vec<(Vec<u8>, ChannelMessageRecord)> = scan_run_rows(
        state,
        context.flow_hash,
        run_id,
        RunTable::ChannelMessage,
        &suffix_prefix,
    )?;
    let mut messages = Vec::with_capacity(rows.len());
    for (_, record) in rows {
        let value = match record.value.as_ref() {
            Some(stored) => response_value(state, context.flow_hash, run_id, stored, is_hydrating)?,
            None => None,
        };
        messages.push(ChannelMessage {
            value: match value {
                Some(WireValue::Value(value)) => Some(value),
                Some(WireValue::HardDeleteObject(_)) | None => None,
            },
            channel_name: record.channel_name,
            message_id: record.message_id,
        });
    }
    Ok(messages)
}

/// Ordinary Attributes are always loaded; AttributeMap instances only when selected exactly
/// or by a `definition/` prefix (plan 9.3).
fn is_selected(key: &str, selections: &[String]) -> bool {
    if !key.contains('/') {
        return true;
    }
    selections.iter().any(|selection| {
        if selection.ends_with('/') {
            key.starts_with(selection.as_str())
        } else {
            key == selection
        }
    })
}

fn sorted_unique(values: &[String]) -> Vec<String> {
    let set: BTreeSet<&String> = values.iter().collect();
    set.into_iter().cloned().collect()
}

fn has_rpc_effects(response: &InvokeWorkerRpcResponse) -> bool {
    let has_step_decision = response.step_decision.as_ref().is_some_and(|decision| {
        !decision.next_steps.is_empty() || !decision.cancel_step_types.is_empty()
    });
    has_step_decision
        || !response.upsert_attributes.is_empty()
        || !response.record_events.is_empty()
        || !response.delete_from_channel.is_empty()
        || !response.publish_to_channel.is_empty()
        || response
            .action_permission_mappings
            .as_ref()
            .is_some_and(|mappings| !mappings.mappings.is_empty())
}

fn rpc_invoke_error(error: RpcInvokeError) -> FlowCoordinationError {
    match error {
        RpcInvokeError::WorkerError { response } => FlowCoordinationError::WorkerRpcFailed {
            worker_error: response,
        },
        RpcInvokeError::NoWorkerAvailable { detail }
        | RpcInvokeError::WorkerDisconnected { detail } => {
            FlowCoordinationError::Unavailable { detail }
        }
        RpcInvokeError::UnknownRpc { detail } => FlowCoordinationError::rejected(
            grpc_code::FAILED_PRECONDITION,
            ErrorSubStatus::Uncategorized,
            detail,
        ),
        RpcInvokeError::DeadlineExceeded => FlowCoordinationError::rejected(
            grpc_code::DEADLINE_EXCEEDED,
            ErrorSubStatus::Uncategorized,
            "worker RPC deadline exceeded",
        ),
    }
}
