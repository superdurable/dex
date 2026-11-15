// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! CloneForkFlow: the owner computes the source point's copy, stages it on the new Flow's
//! partition, then commits the FlowCloneFork that creates the new run there.
//!
//! The source run is only read. Apply rejects a replaced or expired staging with ABORTED, and
//! the owner computes and stages again.

use std::sync::Arc;

use dexzd_apply::{
    ApplyContext, CloneForkComputation, CloneForkComputationRequest, ComputedCloneFork,
    MissingHistorySegments, SegmentedCloneForkComputation, StateChildFlowRuns,
};
use dexzd_keyspace::{RunId, TenantId, ValueId};
use dexzd_protocol::dex::{
    AttributeWrite, CloneForkFlowRequest, CloneForkFlowResponse, ErrorSubStatus,
    FlowCloneForkSource,
};
use dexzd_protocol::dexzd::v1::{
    FlowCloneForkCommand, HistorySegmentRecord, RunRecord, command_reply, flow_command,
};
use dexzd_request_validation::{
    ClientRequest, validate_client_request, validate_clone_fork_flow_config,
};
use xxhash_rust::xxh3::xxh3_128;

use super::FLOW_TIMEOUT_POLICY_FAIL;
use super::hard_delete_uploads::hard_delete_references;
use super::time_travel::{
    FIRST_RECOMPUTATION_BACKOFF, MAX_HISTORY_READ_ROUNDS, MAX_TIME_TRAVEL_COMPUTATIONS,
    TimeTravelSegmentReads,
};
use super::{unexpected_reply, unstamped_flow_command};
use crate::flow_coordination::read_operations::state_rows::{read_directory, read_run};
use crate::flow_coordination::{
    FlowAddress, FlowCoordinationError, FlowCoordinatorRegistry, grpc_code,
};

/// How one clone fork attempt ended.
enum CloneForkOutcome {
    /// The FlowCloneFork committed, or the request was a retry.
    Done(CloneForkFlowResponse),
    /// Apply found the staging replaced or expired; staging again may succeed.
    Stale(FlowCoordinationError),
}

/// How one committed view round of a clone fork computation ended.
enum CloneForkRound {
    Computed(CloneForkComputation),
    /// The round reached uploaded segments it lacks.
    NeedsSegments(MissingHistorySegments),
    /// The round read values that offload deleted locally, which it lacks.
    NeedsValues(Vec<(ValueId, HistorySegmentRecord)>),
}

impl FlowCoordinatorRegistry {
    /// Creates the Flow `request.flow_id` with a run that starts from a copy of the source run's
    /// state at the requested point, and returns the new run once it is committed.
    ///
    /// Clone forks into one Flow run one at a time; one clone fork computes and stages its copy
    /// at most four times.
    pub async fn clone_fork_flow(
        &self,
        tenant_id: TenantId,
        request: CloneForkFlowRequest,
    ) -> Result<CloneForkFlowResponse, FlowCoordinationError> {
        validate_client_request(
            ClientRequest::CloneForkFlow(&request),
            &self.inner.config.validation,
        )?;
        let source = FlowAddress::new(tenant_id, request.source_flow_id.clone());
        let target = FlowAddress::new(tenant_id, request.flow_id.clone());
        // Retries of one request reuse one staging ID, so they must not stage concurrently.
        let lease = self.lease(&target);
        let _turn = lease.coordinator().lock_time_travel_turn().await;
        let mut backoff = FIRST_RECOMPUTATION_BACKOFF;
        let mut attempts = 1;
        loop {
            let stale = match self.clone_fork_once(&source, &target, &request).await? {
                CloneForkOutcome::Done(response) => return Ok(response),
                CloneForkOutcome::Stale(stale) => stale,
            };
            if attempts == MAX_TIME_TRAVEL_COMPUTATIONS {
                return Err(stale);
            }
            tracing::info!(
                flow_id = %target.flow_id,
                attempts,
                reason = %stale,
                "clone fork staging is out of date; staging it again"
            );
            tokio::time::sleep(backoff).await;
            backoff *= 2;
            attempts += 1;
        }
    }

    /// Computes, stages, and commits one clone fork.
    ///
    /// Returns [`CloneForkOutcome::Stale`] for an ABORTED rejection, and other rejections and
    /// failures as errors.
    async fn clone_fork_once(
        &self,
        source: &FlowAddress,
        target: &FlowAddress,
        request: &CloneForkFlowRequest,
    ) -> Result<CloneForkOutcome, FlowCoordinationError> {
        self.check_running()?;
        if let Some(response) = self.retried_clone_fork(target, request)? {
            return Ok(CloneForkOutcome::Done(response));
        }
        let source_context = self.flow_context(source);
        let view = self.inner.dependencies.committer.committed_view()?;
        let source_run_id = read_run(
            view.as_ref(),
            source_context,
            source,
            &request.source_run_id,
        )?
        .run_id;
        let computed = self
            .compute_clone_fork(source, view, source_context, source_run_id, request)
            .await?;
        let flow_config = validate_clone_fork_flow_config(
            computed.source_flow_config(),
            request.flow_config_override.as_ref(),
            &self.inner.config.validation,
        )?;
        let settings = self.inner.config.run_settings;
        if computed.state_bytes() > settings.max_run_state_bytes {
            return Err(FlowCoordinationError::rejected(
                grpc_code::RESOURCE_EXHAUSTED,
                ErrorSubStatus::Uncategorized,
                format!(
                    "the copied state of {} bytes exceeds the maximum active run state of {} \
                     bytes (limits.maxRunStateBytes)",
                    computed.state_bytes(),
                    settings.max_run_state_bytes
                ),
            ));
        }
        let staging_id = clone_fork_staging_id(target, &request.request_id);
        let stage_commands = computed.stage_commands(&staging_id).map_err(|error| {
            FlowCoordinationError::rejected(
                grpc_code::RESOURCE_EXHAUSTED,
                ErrorSubStatus::Uncategorized,
                error.to_string(),
            )
        })?;
        for stage in stage_commands {
            let command = unstamped_flow_command(
                target,
                None,
                flow_command::Command::FlowCloneForkStage(stage),
            );
            match self
                .propose_client_command(target, |_| Ok(command.clone()))
                .await
            {
                Ok(command_reply::Reply::Ok(_)) => {}
                Ok(other) => return Err(unexpected_reply("FlowCloneForkStage", &other)),
                Err(error) => return stale_or_error(error),
            }
        }
        let new_run_id = RunId(self.inner.dependencies.id_source.run_id().into_bytes());
        let mut writes = self
            .copied_hard_delete_writes(source, source_run_id, &computed)
            .await?;
        let copied_count = writes.len();
        writes.extend(request.attributes.iter().cloned());
        let uploads = self
            .upload_hard_delete_values(target, new_run_id, &mut writes)
            .await?;
        let request_attributes = writes.split_off(copied_count);
        let duration_micros = computed.flow_timeout_duration_micros();
        let resolved_timeout_policy = if duration_micros > 0 && computed.timeout_policy() == 0 {
            FLOW_TIMEOUT_POLICY_FAIL
        } else {
            computed.timeout_policy()
        };
        let clone_fork = FlowCloneForkCommand {
            staging_id,
            new_run_id: new_run_id.as_bytes().to_vec(),
            request: Some(CloneForkFlowRequest {
                attributes: request_attributes,
                ..request.clone()
            }),
            settings: Some(settings),
            effective_flow_config: Some(flow_config),
            flow_timeout_duration_micros: duration_micros,
            resolved_timeout_policy,
            timeout_handler_options: computed.timeout_handler_options().cloned(),
            hard_delete_objects: hard_delete_references(uploads.as_ref()),
            copied_hard_delete_attributes: writes,
        };
        let command = unstamped_flow_command(
            target,
            None,
            flow_command::Command::FlowCloneFork(clone_fork),
        );
        let reply = match self
            .propose_client_command_with_uploads(target, uploads, |_| Ok(command.clone()))
            .await
        {
            Ok(reply) => reply,
            Err(error) => return stale_or_error(error),
        };
        let command_reply::Reply::StartFlow(started) = reply else {
            return Err(unexpected_reply("CloneForkFlow", &reply));
        };
        let run_id = RunId::from_slice(&started.run_id)
            .map_err(|error| FlowCoordinationError::corrupt_row("clone fork reply", error))?;
        if started.is_existing_run {
            return self
                .existing_run_response(target, run_id)
                .map(CloneForkOutcome::Done);
        }
        Ok(CloneForkOutcome::Done(response(run_id, computed.source())))
    }

    /// Answers a retry of the request that started the target's latest run, as StartFlow does,
    /// without computing a copy.
    ///
    /// Returns `None` when the latest run did not start from this request. Returns
    /// ALREADY_EXISTS when the request does not ignore the already-started error.
    fn retried_clone_fork(
        &self,
        target: &FlowAddress,
        request: &CloneForkFlowRequest,
    ) -> Result<Option<CloneForkFlowResponse>, FlowCoordinationError> {
        let view = self.inner.dependencies.committer.committed_view()?;
        let Some(directory) = read_directory(view.as_ref(), self.flow_context(target), target)?
        else {
            return Ok(None);
        };
        let Some(latest) = directory.runs.last() else {
            return Ok(None);
        };
        if latest.start_request_id != request.request_id {
            return Ok(None);
        }
        let should_return_existing_run = request
            .flow_already_started_options
            .as_ref()
            .is_some_and(|options| options.ignore_already_started_error);
        if !should_return_existing_run {
            return Err(FlowCoordinationError::rejected(
                grpc_code::ALREADY_EXISTS,
                ErrorSubStatus::FlowAlreadyStarted,
                format!("Flow {} is already started", target.flow_id),
            ));
        }
        let run_id = RunId::from_slice(&latest.run_id)
            .map_err(|error| FlowCoordinationError::corrupt_row("Flow directory", error))?;
        self.existing_run_response(target, run_id).map(Some)
    }

    /// Returns the response for the existing run `run_id` of `target`, with the source point
    /// it records.
    fn existing_run_response(
        &self,
        target: &FlowAddress,
        run_id: RunId,
    ) -> Result<CloneForkFlowResponse, FlowCoordinationError> {
        let view = self.inner.dependencies.committer.committed_view()?;
        let record: RunRecord = read_run(
            view.as_ref(),
            self.flow_context(target),
            target,
            &run_id.to_string(),
        )?
        .record;
        Ok(response(
            run_id,
            &record.clone_fork_source.unwrap_or_default(),
        ))
    }

    /// Reads the values of the source run's current hard-delete Attributes, which the new run
    /// stores in objects of its own.
    ///
    /// Returns FAILED_PRECONDITION when an object no longer exists, and UNAVAILABLE when object
    /// storage fails.
    async fn copied_hard_delete_writes(
        &self,
        source: &FlowAddress,
        source_run_id: RunId,
        computed: &ComputedCloneFork,
    ) -> Result<Vec<AttributeWrite>, FlowCoordinationError> {
        let reader = &self.inner.dependencies.hard_delete_reader;
        let mut writes = Vec::with_capacity(computed.hard_delete_attributes().len());
        for attribute in computed.hard_delete_attributes() {
            let value = reader
                .read_hard_delete_value(source, source_run_id, &attribute.object)
                .await
                .map_err(|error| FlowCoordinationError::Unavailable {
                    detail: format!(
                        "reading hard-delete Attribute {:?} of Flow {} failed: {error}",
                        attribute.key, source.flow_id
                    ),
                })?;
            let Some(value) = value else {
                return Err(FlowCoordinationError::rejected(
                    grpc_code::FAILED_PRECONDITION,
                    ErrorSubStatus::Uncategorized,
                    format!(
                        "hard-delete Attribute {:?} of Flow {} no longer has its object",
                        attribute.key, source.flow_id
                    ),
                ));
            };
            writes.push(AttributeWrite {
                key: attribute.key.clone(),
                value: Some(value),
                is_hard_delete_on_null: true,
                ..AttributeWrite::default()
            });
        }
        Ok(writes)
    }

    /// Computes the copy on blocking threads in committed view rounds, the first on `view`.
    /// Between rounds it fetches the uploaded segments and offloaded values the last round
    /// reached, holding no view.
    ///
    /// Returns the computation's rejection, or a failure to read history, as an error.
    async fn compute_clone_fork(
        &self,
        source: &FlowAddress,
        view: Box<dyn dexzd_apply::FlowStateReader + Send>,
        context: ApplyContext,
        source_run_id: RunId,
        request: &CloneForkFlowRequest,
    ) -> Result<ComputedCloneFork, FlowCoordinationError> {
        let now_micros = self.inner.dependencies.clock.now_unix_micros();
        let mut reads =
            TimeTravelSegmentReads::new(self.inner.config.time_travel_replay_memory_bytes);
        let mut first_view = Some(view);
        let mut segment_rounds = 0;
        loop {
            let view = match first_view.take() {
                Some(view) => view,
                None => self.inner.dependencies.committer.committed_view()?,
            };
            match self
                .clone_fork_round(view, context, source_run_id, request, now_micros, &reads)
                .await?
            {
                CloneForkRound::Computed(CloneForkComputation::Computed(computed)) => {
                    return Ok(*computed);
                }
                CloneForkRound::Computed(CloneForkComputation::Rejected(reply)) => {
                    return Err(match reply.reply {
                        Some(command_reply::Reply::Error(error)) => {
                            FlowCoordinationError::from_reply(&error)
                        }
                        other => FlowCoordinationError::Internal {
                            detail: format!(
                                "a clone fork computation rejected with reply {other:?}"
                            ),
                        },
                    });
                }
                CloneForkRound::NeedsSegments(needed) => {
                    segment_rounds += 1;
                    if segment_rounds == MAX_HISTORY_READ_ROUNDS {
                        return Err(FlowCoordinationError::Unavailable {
                            detail: format!(
                                "the clone fork of flow {} read uploaded history segments in \
                                 {MAX_HISTORY_READ_ROUNDS} rounds without finishing; retry",
                                source.flow_id
                            ),
                        });
                    }
                    self.load_time_travel_segments(source, source_run_id, &needed, &mut reads)
                        .await?;
                }
                // Each such round must fetch something new; the memory budget bounds them all.
                CloneForkRound::NeedsValues(needed) => {
                    let is_progress = self
                        .load_offloaded_values(source, source_run_id, needed, &mut reads)
                        .await?;
                    if !is_progress {
                        return Err(FlowCoordinationError::Internal {
                            detail: format!(
                                "the clone fork of flow {} asked again for offloaded values it \
                                 already fetched",
                                source.flow_id
                            ),
                        });
                    }
                }
            }
        }
    }

    /// Opens the source run's history from `view` with what `reads` fetched, and computes the
    /// copy on a blocking thread, which releases the view.
    async fn clone_fork_round(
        &self,
        view: Box<dyn dexzd_apply::FlowStateReader + Send>,
        context: ApplyContext,
        source_run_id: RunId,
        request: &CloneForkFlowRequest,
        now_micros: i64,
        reads: &TimeTravelSegmentReads,
    ) -> Result<CloneForkRound, FlowCoordinationError> {
        let history = self
            .inner
            .dependencies
            .time_travel_history
            .open_time_travel_history(
                view,
                context,
                source_run_id,
                Arc::clone(&reads.segments),
                Arc::clone(&reads.values),
            )
            .await
            .map_err(|error| FlowCoordinationError::Unavailable {
                detail: error.to_string(),
            })?;
        let clone_fork = request.clone();
        let memory_budget_bytes = self.inner.config.time_travel_replay_memory_bytes;
        // Child Flows may live on other partitions; this node's store holds them all for now.
        let child_view = self.inner.dependencies.committer.committed_view()?;
        let hasher = self.inner.dependencies.hasher.clone();
        let (computation, missing_values) = self
            .inner
            .config
            .blocking_work
            .run(move || {
                let child_flow_runs = StateChildFlowRuns::new(child_view.as_ref(), &hasher);
                let computation = history.compute_clone_fork(
                    &context,
                    &CloneForkComputationRequest {
                        source_run_id,
                        clone_fork: &clone_fork,
                        now_micros,
                        memory_budget_bytes,
                    },
                    &child_flow_runs,
                );
                (computation, history.take_missing_offloaded_values())
            })
            .await
            .map_err(|failure| FlowCoordinationError::Internal {
                detail: format!("clone fork computation for run {source_run_id} failed: {failure}"),
            })?;
        if !missing_values.is_empty() {
            return Ok(CloneForkRound::NeedsValues(missing_values));
        }
        Ok(match computation? {
            SegmentedCloneForkComputation::Computed(computation) => {
                CloneForkRound::Computed(computation)
            }
            SegmentedCloneForkComputation::NeedsSegments(needed) => {
                CloneForkRound::NeedsSegments(needed)
            }
        })
    }
}

/// Returns [`CloneForkOutcome::Stale`] for an ABORTED rejection, which staging again settles,
/// and `error` otherwise.
fn stale_or_error(error: FlowCoordinationError) -> Result<CloneForkOutcome, FlowCoordinationError> {
    if error.grpc_code() == grpc_code::ABORTED {
        Ok(CloneForkOutcome::Stale(error))
    } else {
        Err(error)
    }
}

/// The staging ID of one request: it is the same for every retry of the request, so a retry
/// replaces an abandoned staging instead of adding one.
fn clone_fork_staging_id(target: &FlowAddress, request_id: &str) -> Vec<u8> {
    let mut identity = u64::try_from(target.flow_id.len())
        .expect("a Flow ID length fits a u64")
        .to_be_bytes()
        .to_vec();
    identity.extend_from_slice(target.flow_id.as_bytes());
    identity.extend_from_slice(request_id.as_bytes());
    xxh3_128(&identity).to_be_bytes().to_vec()
}

fn response(run_id: RunId, source: &FlowCloneForkSource) -> CloneForkFlowResponse {
    CloneForkFlowResponse {
        run_id: run_id.to_string(),
        source_run_id: source.run_id.clone(),
        source_run_attempt: source.run_attempt,
        source_event_id: source.event_id,
    }
}
