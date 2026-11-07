// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! ResetFlow (plan section 6.3): the owner computes the result, stages it, then commits the
//! TimeTravel.
//!
//! Apply rejects a stale staged result with ABORTED, and the owner computes it again.

use std::time::Duration;

use dexzd_apply::{
    ApplyContext, ComputedTimeTravel, TimeTravelComputation, TimeTravelComputationRequest,
    TimeTravelComputationSources, compute_time_travel,
};
use dexzd_keyspace::{RunId, TenantId};
use dexzd_protocol::dex::{ErrorSubStatus, ResetFlowRequest, ResetFlowResponse};
use dexzd_protocol::dexzd::v1::{CommandErrorReply, FlowCommand, command_reply, flow_command};
use dexzd_request_validation::{ClientRequest, validate_client_request};

use crate::flow_coordination::client_operations::{unexpected_reply, unstamped_flow_command};
use crate::flow_coordination::read_operations::state_rows::{read_run, resolve_command_run};
use crate::flow_coordination::{
    FlowAddress, FlowCoordinationError, FlowCoordinatorRegistry, grpc_code,
};

/// Computations one ResetFlow runs at most: the first and three after stale-result rejections.
const MAX_TIME_TRAVEL_COMPUTATIONS: u32 = 4;

/// Wait before the first recomputation; it doubles before each later one.
const FIRST_RECOMPUTATION_BACKOFF: Duration = Duration::from_millis(100);

/// How one computation of a ResetFlow ended.
enum TimeTravelOutcome {
    /// The TimeTravel committed.
    Applied(ResetFlowResponse),
    /// Apply found the staged result out of date; computing it again may succeed.
    Stale(FlowCoordinationError),
}

impl FlowCoordinatorRegistry {
    /// Time-travels a run to an earlier point once the reset is committed. The run ID stays
    /// the same, so the response carries the target run's ID.
    ///
    /// ResetFlows of one Flow run one at a time; one ResetFlow computes its result at most four
    /// times.
    pub async fn reset_flow(
        &self,
        tenant_id: TenantId,
        request: ResetFlowRequest,
    ) -> Result<ResetFlowResponse, FlowCoordinationError> {
        validate_client_request(
            ClientRequest::ResetFlow(&request),
            &self.inner.config.validation,
        )?;
        let flow = FlowAddress::new(tenant_id, request.flow_id.clone());
        let lease = self.lease(&flow);
        let _turn = lease.coordinator().lock_time_travel_turn().await;
        let mut backoff = FIRST_RECOMPUTATION_BACKOFF;
        let mut computations = 1;
        loop {
            let stale = match self.time_travel_once(&flow, &request).await? {
                TimeTravelOutcome::Applied(response) => return Ok(response),
                TimeTravelOutcome::Stale(stale) => stale,
            };
            if computations == MAX_TIME_TRAVEL_COMPUTATIONS {
                return Err(stale);
            }
            tracing::info!(
                flow_id = %flow.flow_id,
                computations,
                reason = %stale,
                "time travel result is out of date; computing it again"
            );
            tokio::time::sleep(backoff).await;
            backoff *= 2;
            computations += 1;
        }
    }

    /// Computes, stages, and applies one time travel result.
    ///
    /// Returns [`TimeTravelOutcome::Stale`] for an ABORTED rejection, and other rejections and
    /// failures as errors.
    async fn time_travel_once(
        &self,
        flow: &FlowAddress,
        request: &ResetFlowRequest,
    ) -> Result<TimeTravelOutcome, FlowCoordinationError> {
        self.check_running()?;
        let context = self.flow_context(flow);
        let view = self.inner.dependencies.committer.committed_view()?;
        let run_id = resolve_command_run(view.as_ref(), context, flow, &request.run_id)?;
        let computed = self.compute(view, context, run_id, request).await?;
        let staging_id = self
            .inner
            .dependencies
            .id_source
            .time_travel_staging_id()
            .as_bytes()
            .to_vec();
        let stage_commands = computed.stage_commands(&staging_id).map_err(|error| {
            FlowCoordinationError::rejected(
                grpc_code::RESOURCE_EXHAUSTED,
                ErrorSubStatus::Uncategorized,
                error.to_string(),
            )
        })?;
        let expected_run_attempt = computed.run_attempt();
        for stage in stage_commands {
            let command = flow_command::Command::TimeTravelStage(stage);
            let reply = self
                .propose_attempt_command(flow, run_id, expected_run_attempt, command)
                .await?;
            match reply {
                command_reply::Reply::Ok(_) => {}
                command_reply::Reply::Error(error) => {
                    return self.stale_or_error(flow, run_id, expected_run_attempt, &error);
                }
                other => return Err(unexpected_reply("TimeTravelStage", &other)),
            }
        }
        let time_travel =
            flow_command::Command::TimeTravel(computed.time_travel_command(&staging_id, true));
        let reply = self
            .propose_attempt_command(flow, run_id, expected_run_attempt, time_travel)
            .await?;
        match reply {
            command_reply::Reply::TimeTravel(time_travel) => {
                Ok(TimeTravelOutcome::Applied(ResetFlowResponse {
                    run_id: run_id.to_string(),
                    run_attempt: i32::try_from(time_travel.run_attempt).map_err(|error| {
                        FlowCoordinationError::corrupt_row("time travel reply", error)
                    })?,
                }))
            }
            command_reply::Reply::Error(error) => {
                self.stale_or_error(flow, run_id, expected_run_attempt, &error)
            }
            other => Err(unexpected_reply("ResetFlow", &other)),
        }
    }

    /// Classifies a rejection of a Stage or TimeTravel proposed at `expected_run_attempt`.
    ///
    /// ABORTED means stale. So does FAILED_PRECONDITION when the run's attempt changed: a
    /// concurrent time travel committed first.
    ///
    /// Returns [`TimeTravelOutcome::Stale`] for a result to compute again, and any other
    /// rejection as an error.
    fn stale_or_error(
        &self,
        flow: &FlowAddress,
        run_id: RunId,
        expected_run_attempt: u32,
        error: &CommandErrorReply,
    ) -> Result<TimeTravelOutcome, FlowCoordinationError> {
        let rejection = FlowCoordinationError::from_reply(error);
        let is_stale = match error.grpc_code {
            grpc_code::ABORTED => true,
            grpc_code::FAILED_PRECONDITION => {
                self.current_run_attempt(flow, run_id)? != Some(expected_run_attempt)
            }
            _ => false,
        };
        if is_stale {
            Ok(TimeTravelOutcome::Stale(rejection))
        } else {
            Err(rejection)
        }
    }

    /// Returns the committed current attempt of run `run_id`, or `None` when it is gone.
    fn current_run_attempt(
        &self,
        flow: &FlowAddress,
        run_id: RunId,
    ) -> Result<Option<u32>, FlowCoordinationError> {
        let view = self.inner.dependencies.committer.committed_view()?;
        match read_run(
            view.as_ref(),
            self.flow_context(flow),
            flow,
            &run_id.to_string(),
        ) {
            Ok(run) => Ok(Some(run.record.current_attempt)),
            Err(error) if error.grpc_code() == grpc_code::NOT_FOUND => Ok(None),
            Err(error) => Err(error),
        }
    }

    /// Opens the run's history from `view` and computes the result on a blocking thread.
    ///
    /// Returns the computation's rejection, or a failure to read history, as an error.
    async fn compute(
        &self,
        view: Box<dyn dexzd_apply::FlowStateReader + Send>,
        context: ApplyContext,
        run_id: RunId,
        request: &ResetFlowRequest,
    ) -> Result<ComputedTimeTravel, FlowCoordinationError> {
        let history = self
            .inner
            .dependencies
            .time_travel_history
            .open_time_travel_history(view, context, run_id)
            .await
            .map_err(|error| FlowCoordinationError::Unavailable {
                detail: error.to_string(),
            })?;
        let reset = request.clone();
        let now_micros = self.inner.dependencies.clock.now_unix_micros();
        let replay_memory_budget_bytes = self.inner.config.time_travel_replay_memory_bytes;
        let computation = self
            .inner
            .config
            .blocking_work
            .run(move || {
                let history_source = history.history();
                compute_time_travel(
                    &context,
                    &TimeTravelComputationSources {
                        state: history.state(),
                        history: history_source.as_ref(),
                        offloaded_values: history.offloaded_values(),
                    },
                    &TimeTravelComputationRequest {
                        run_id,
                        reset: &reset,
                        now_micros,
                        replay_memory_budget_bytes,
                    },
                )
            })
            .await
            .map_err(|failure| FlowCoordinationError::Internal {
                detail: format!("time travel computation for run {run_id} failed: {failure}"),
            })??;
        match computation {
            TimeTravelComputation::Computed(computed) => Ok(*computed),
            TimeTravelComputation::Rejected(reply) => match reply.reply {
                Some(command_reply::Reply::Error(error)) => {
                    Err(FlowCoordinationError::from_reply(&error))
                }
                other => Err(FlowCoordinationError::Internal {
                    detail: format!("a time travel computation rejected with reply {other:?}"),
                }),
            },
        }
    }

    /// Proposes `command` for run `run_id` while it is at `expected_run_attempt`, and returns
    /// its committed reply.
    async fn propose_attempt_command(
        &self,
        flow: &FlowAddress,
        run_id: RunId,
        expected_run_attempt: u32,
        command: flow_command::Command,
    ) -> Result<command_reply::Reply, FlowCoordinationError> {
        let committed = self
            .propose_and_wait_for_commit(flow, |_| {
                Ok(FlowCommand {
                    expected_run_attempt,
                    ..unstamped_flow_command(flow, Some(run_id), command.clone())
                })
            })
            .await?;
        committed
            .outcome
            .reply
            .reply
            .ok_or_else(|| FlowCoordinationError::Internal {
                detail: "apply returned an empty reply".to_owned(),
            })
    }
}
