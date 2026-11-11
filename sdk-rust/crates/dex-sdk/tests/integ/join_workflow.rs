// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::sync::{Arc, LazyLock};
use std::time::Duration;

use dex_sdk::{
    Attribute, Channel, ChannelMap, Context, Flow, HandlerError, HandlerResult, Join, JoinRound,
    PersistenceSchema, RetryPolicy, Rpc, RpcList, RpcResult, Step, StepDecision, StepList,
    StepMovement, StepOptions, Timer, Wait, WaitForFailurePolicy,
};
use serde::{Deserialize, Serialize};

use crate::step_cancellation_workflow::Event;

pub(crate) static BRANCH_RESULTS: LazyLock<Join<String>> =
    LazyLock::new(|| Join::new("rust-join-branch-results"));
pub(crate) static BRANCH_RELEASES: LazyLock<ChannelMap<()>> =
    LazyLock::new(|| ChannelMap::new("rust-join-branch-releases"));
pub(crate) static FINISH: LazyLock<Channel<()>> =
    LazyLock::new(|| Channel::new("rust-join-finish"));
pub(crate) static LATE_RESULTS: LazyLock<Attribute<Vec<String>>> =
    LazyLock::new(|| Attribute::new("rust-join-late-results"));
pub(crate) static FIRST_ROUND_RESULTS: LazyLock<Attribute<Vec<String>>> =
    LazyLock::new(|| Attribute::new("rust-join-first-round-results"));

type BranchBuilder<'builder> = Box<dyn FnOnce(&JoinRound) -> StepMovement + 'builder>;

/// The round key of every round that the starting Step forks.
pub(crate) const START_ROUND_KEY: &str = "StartJoinScenario-1";

#[derive(Clone, Debug, Serialize, Deserialize)]
pub(crate) enum JoinScenario {
    AllOfBeforeWait,
    MultipleInstances(Vec<String>),
    PartialJoinRounds,
    KeyIsolation,
    CancellingFirstBranch,
    CancelJoinWithBranches,
    DoubleFork,
    RpcFork,
    InvalidConditions,
    ZeroBranches,
    TimerDeadline,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub(crate) struct BranchInput {
    label: String,
    round: JoinRound,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub(crate) struct JoinOutput {
    pub(crate) round_key: String,
    pub(crate) branch_count: usize,
    pub(crate) results: Vec<String>,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub(crate) struct RoundRequest {
    round_number: u32,
    previous_round: Option<JoinRound>,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub(crate) struct PartialRoundJoinInput {
    round_number: u32,
    round: JoinRound,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub(crate) struct PartialJoinRoundsOutput {
    pub(crate) first_round_results: Vec<String>,
    pub(crate) late_first_round_results: Vec<String>,
    pub(crate) second_round_key: String,
    pub(crate) second_round_results: Vec<String>,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub(crate) struct BranchReport {
    pub(crate) round: JoinRound,
    pub(crate) result: String,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub(crate) struct RpcForkOutput {
    pub(crate) fork_error: String,
    pub(crate) slash_key_error: String,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub(crate) enum ConditionProbeCase {
    BranchesZero,
    BranchesAboveCount,
    FirstBranchOfZeroBranches,
    RemainingBranchesAboveCount,
    RemainingBranchesNoneLeft,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub(crate) struct ConditionProbeInput {
    remaining_cases: Vec<ConditionProbeCase>,
    failed_cases: Vec<ConditionProbeCase>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub(crate) struct DeadlineOutput {
    pub(crate) has_timer_fired: bool,
    pub(crate) results: Vec<String>,
}

#[derive(Default)]
pub(crate) struct JoinTestState {
    pub(crate) cancelling_join_release: Event,
}

pub(crate) struct JoinWorkflow {
    start: StartJoinScenario,
    start_failure: ReportStartFailure,
    released_branch: ReleasedBranch,
    handoff_branch: HandoffBranch,
    join_gate: JoinGate,
    all_of_join: AllOfJoin,
    request_round: RequestRound,
    partial_round_join: PartialRoundJoin,
    cancelling_first_join: CancellingFirstJoin,
    hold_until_finished: HoldUntilFinished,
    condition_probe: ConditionProbe,
    deadline_join: DeadlineJoin,
}

impl JoinWorkflow {
    pub(crate) const RELEASE_BRANCH: Rpc<String, ()> = Rpc::new("release_branch");
    pub(crate) const REPORT_BRANCH_RESULT: Rpc<BranchReport, ()> = Rpc::new("report_branch_result");
    pub(crate) const CANCEL_JOIN_WITH_BRANCHES: Rpc<(), ()> = Rpc::new("cancel_join_with_branches");
    pub(crate) const FORK_JOIN_ROUND: Rpc<String, RpcForkOutput> = Rpc::new("fork_join_round");
    pub(crate) const GET_PENDING_RESULTS: Rpc<String, Vec<String>> =
        Rpc::new("get_pending_results");
    pub(crate) const GET_PENDING_RELEASE_COUNT: Rpc<String, usize> =
        Rpc::new("get_pending_release_count");
    pub(crate) const FINISH_FLOW: Rpc<(), ()> = Rpc::new("finish_flow");

    pub(crate) const JOIN_CANCELED: &str = "join canceled";

    pub(crate) fn new(state: Arc<JoinTestState>) -> Self {
        Self {
            start: StartJoinScenario(Arc::clone(&state)),
            start_failure: ReportStartFailure,
            released_branch: ReleasedBranch,
            handoff_branch: HandoffBranch,
            join_gate: JoinGate,
            all_of_join: AllOfJoin,
            request_round: RequestRound,
            partial_round_join: PartialRoundJoin,
            cancelling_first_join: CancellingFirstJoin(state),
            hold_until_finished: HoldUntilFinished,
            condition_probe: ConditionProbe,
            deadline_join: DeadlineJoin,
        }
    }

    fn release_branch(&self, context: &mut Context, label: String) -> HandlerResult<()> {
        BRANCH_RELEASES.publish(context, &label, ())
    }

    fn report_branch_result(
        &self,
        context: &mut Context,
        report: BranchReport,
    ) -> HandlerResult<()> {
        BRANCH_RESULTS.report_branch_result(context, &report.round, report.result)
    }

    fn cancel_join_with_branches(&self, _context: &mut Context) -> HandlerResult<RpcResult<()>> {
        Ok(RpcResult::new(())
            .cancel_step(&self.all_of_join)
            .cancel_step(&self.released_branch)
            .then(StepMovement::to(
                &self.hold_until_finished,
                JoinWorkflow::JOIN_CANCELED.to_string(),
            )))
    }

    fn fork_join_round(
        &self,
        context: &mut Context,
        round_key: String,
    ) -> HandlerResult<RpcResult<RpcForkOutput>> {
        let fork_error = match BRANCH_RESULTS.fork(context, Vec::new(), |round| {
            StepMovement::to(&self.hold_until_finished, round.round_key)
        }) {
            Ok(_) => return Err(HandlerError::new("JoinFailure", "RPC fork succeeded")),
            Err(error) => error.to_string(),
        };
        let slash_key_error =
            match BRANCH_RESULTS.fork_with_round_key("tenant/round", Vec::new(), |round| {
                StepMovement::to(&self.hold_until_finished, round.round_key)
            }) {
                Ok(_) => return Err(HandlerError::new("JoinFailure", "slash key accepted")),
                Err(error) => error.to_string(),
            };
        let mut branch_builders = released_branch_builders(&["rpc-a"]);
        // The handoff branch ends without reporting; a later RPC call reports its result.
        branch_builders.push(Box::new(|round: &JoinRound| {
            StepMovement::to(&self.handoff_branch, round.clone())
        }));
        let movements =
            BRANCH_RESULTS.fork_with_round_key(&round_key, branch_builders, |round| {
                StepMovement::to(&self.all_of_join, round)
            })?;
        let output = RpcForkOutput {
            fork_error,
            slash_key_error,
        };
        Ok(movements
            .into_iter()
            .fold(RpcResult::new(output), RpcResult::then))
    }

    fn get_pending_results(
        &self,
        context: &mut Context,
        round_key: String,
    ) -> HandlerResult<RpcResult<Vec<String>>> {
        let pending_results = BRANCH_RESULTS
            .channel_map()
            .pending_messages(context, &round_key)?
            .into_iter()
            .map(|message| message.value)
            .collect();
        Ok(RpcResult::new(pending_results))
    }

    fn get_pending_release_count(
        &self,
        context: &mut Context,
        label: String,
    ) -> HandlerResult<RpcResult<usize>> {
        let pending_releases = BRANCH_RELEASES.pending_messages(context, &label)?;
        Ok(RpcResult::new(pending_releases.len()))
    }

    fn finish_flow(&self, context: &mut Context) -> HandlerResult<()> {
        FINISH.publish(context, ())
    }
}

impl Flow for JoinWorkflow {
    type StartInput = JoinScenario;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
            .and(&self.start_failure)
            .and(&self.released_branch)
            .and(&self.handoff_branch)
            .and(&self.join_gate)
            .and(&self.all_of_join)
            .and(&self.request_round)
            .and(&self.partial_round_join)
            .and(&self.cancelling_first_join)
            .and(&self.hold_until_finished)
            .and(&self.condition_probe)
            .and(&self.deadline_join)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .join(&BRANCH_RESULTS)
            .channel_map(&BRANCH_RELEASES)
            .channel(&FINISH)
            .attribute(&LATE_RESULTS)
            .attribute(&FIRST_ROUND_RESULTS)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure(Self::RELEASE_BRANCH, Self::release_branch)
            .procedure(Self::REPORT_BRANCH_RESULT, Self::report_branch_result)
            .function_without_input(
                Self::CANCEL_JOIN_WITH_BRANCHES,
                Self::cancel_join_with_branches,
            )
            .function(Self::FORK_JOIN_ROUND, Self::fork_join_round)
            .function(
                Self::GET_PENDING_RESULTS.load_channel_map(BRANCH_RESULTS.channel_map()),
                Self::get_pending_results,
            )
            .function(
                Self::GET_PENDING_RELEASE_COUNT.load_channel_map(&BRANCH_RELEASES),
                Self::get_pending_release_count,
            )
            .procedure_without_input(Self::FINISH_FLOW, Self::finish_flow)
    }
}

fn released_branch_builders<'label>(labels: &[&'label str]) -> Vec<BranchBuilder<'label>> {
    labels
        .iter()
        .map(|&label| {
            Box::new(move |round: &JoinRound| {
                StepMovement::to(
                    &ReleasedBranch,
                    BranchInput {
                        label: label.to_string(),
                        round: round.clone(),
                    },
                )
            }) as BranchBuilder<'label>
        })
        .collect()
}

pub(crate) struct StartJoinScenario(Arc<JoinTestState>);

impl Step for StartJoinScenario {
    type Input = JoinScenario;

    fn execute(
        &self,
        context: &mut Context,
        scenario: JoinScenario,
    ) -> HandlerResult<StepDecision> {
        let all_of_join_movement = |round: JoinRound| StepMovement::to(&AllOfJoin, round);
        let movements = match scenario {
            JoinScenario::AllOfBeforeWait => BRANCH_RESULTS.fork(
                context,
                released_branch_builders(&["A", "B", "C"]),
                |round| StepMovement::to(&JoinGate, round),
            )?,
            JoinScenario::MultipleInstances(reviewers) => {
                let branch_builders = reviewers
                    .into_iter()
                    .map(|reviewer| {
                        Box::new(move |round: &JoinRound| {
                            StepMovement::to(
                                &ReleasedBranch,
                                BranchInput {
                                    label: reviewer,
                                    round: round.clone(),
                                },
                            )
                        }) as BranchBuilder<'static>
                    })
                    .collect();
                BRANCH_RESULTS.fork(context, branch_builders, all_of_join_movement)?
            }
            JoinScenario::PartialJoinRounds => {
                return Ok(StepDecision::go_to(
                    &RequestRound,
                    RoundRequest {
                        round_number: 1,
                        previous_round: None,
                    },
                ));
            }
            JoinScenario::KeyIsolation => BRANCH_RESULTS.fork(
                context,
                released_branch_builders(&["isolated-a", "isolated-b"]),
                all_of_join_movement,
            )?,
            JoinScenario::CancellingFirstBranch => BRANCH_RESULTS.fork(
                context,
                released_branch_builders(&["first-a", "first-b", "first-c"]),
                |round| StepMovement::to(&CancellingFirstJoin(Arc::clone(&self.0)), round),
            )?,
            JoinScenario::CancelJoinWithBranches => BRANCH_RESULTS.fork(
                context,
                released_branch_builders(&["cancel-a", "cancel-b"]),
                all_of_join_movement,
            )?,
            JoinScenario::DoubleFork => {
                BRANCH_RESULTS.fork(context, Vec::new(), all_of_join_movement)?;
                BRANCH_RESULTS.fork(context, Vec::new(), all_of_join_movement)?
            }
            JoinScenario::RpcFork => return Ok(StepDecision::dead_end()),
            JoinScenario::InvalidConditions => {
                return Ok(StepDecision::go_to(
                    &ConditionProbe,
                    ConditionProbeInput {
                        remaining_cases: vec![
                            ConditionProbeCase::BranchesZero,
                            ConditionProbeCase::BranchesAboveCount,
                            ConditionProbeCase::FirstBranchOfZeroBranches,
                            ConditionProbeCase::RemainingBranchesAboveCount,
                            ConditionProbeCase::RemainingBranchesNoneLeft,
                        ],
                        failed_cases: Vec::new(),
                    },
                ));
            }
            JoinScenario::ZeroBranches => {
                BRANCH_RESULTS.fork(context, Vec::new(), all_of_join_movement)?
            }
            JoinScenario::TimerDeadline => BRANCH_RESULTS.fork(
                context,
                released_branch_builders(&["deadline-a", "deadline-b"]),
                |round| StepMovement::to(&DeadlineJoin, round),
            )?,
        };
        Ok(StepDecision::go_to_many(movements))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_retry(RetryPolicy::new().maximum_attempts(1))
            .on_execute_failure_proceed_to(&ReportStartFailure)
    }
}

pub(crate) struct ReportStartFailure;

impl Step for ReportStartFailure {
    type Input = JoinScenario;

    fn execute(
        &self,
        context: &mut Context,
        _scenario: JoinScenario,
    ) -> HandlerResult<StepDecision> {
        let detail = context
            .recovery_error()
            .map(|error| error.detail().to_string())
            .unwrap_or_default();
        Ok(StepDecision::graceful_complete(detail))
    }
}

pub(crate) struct ReleasedBranch;

impl Step for ReleasedBranch {
    type Input = BranchInput;

    fn wait_for(&self, _context: &mut Context, input: BranchInput) -> HandlerResult<Wait> {
        Ok(Wait::until(BRANCH_RELEASES.for_one(&input.label)))
    }

    fn execute(&self, context: &mut Context, input: BranchInput) -> HandlerResult<StepDecision> {
        BRANCH_RESULTS.end_branch(context, &input.round, input.label)
    }
}

/// Ends without reporting; an RPC reports this branch's result.
pub(crate) struct HandoffBranch;

impl Step for HandoffBranch {
    type Input = JoinRound;

    fn execute(&self, _context: &mut Context, _round: JoinRound) -> HandlerResult<StepDecision> {
        Ok(StepDecision::dead_end())
    }
}

/// Starts the all-of join only after the test opens the gate.
pub(crate) struct JoinGate;

impl JoinGate {
    pub(crate) const GATE_LABEL: &str = "join-gate";
}

impl Step for JoinGate {
    type Input = JoinRound;

    fn wait_for(&self, _context: &mut Context, _round: JoinRound) -> HandlerResult<Wait> {
        Ok(Wait::until(BRANCH_RELEASES.for_one(Self::GATE_LABEL)))
    }

    fn execute(&self, _context: &mut Context, round: JoinRound) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&AllOfJoin, round))
    }
}

pub(crate) struct AllOfJoin;

impl Step for AllOfJoin {
    type Input = JoinRound;

    fn wait_for(&self, _context: &mut Context, round: JoinRound) -> HandlerResult<Wait> {
        Ok(Wait::until(BRANCH_RESULTS.for_all_branches(&round)))
    }

    fn execute(&self, context: &mut Context, round: JoinRound) -> HandlerResult<StepDecision> {
        let results = BRANCH_RESULTS.branch_results(context, &round)?;
        Ok(StepDecision::graceful_complete(JoinOutput {
            round_key: round.round_key,
            branch_count: round.branch_count,
            results,
        }))
    }
}

pub(crate) struct RequestRound;

impl RequestRound {
    const REQUIRED_RESULTS: usize = 2;
}

impl Step for RequestRound {
    type Input = RoundRequest;

    fn wait_for(&self, _context: &mut Context, request: RoundRequest) -> HandlerResult<Wait> {
        match &request.previous_round {
            None => Ok(Wait::skip_immediately()),
            Some(previous_round) => Ok(Wait::until(
                BRANCH_RESULTS.for_remaining_branches(previous_round, Self::REQUIRED_RESULTS),
            )),
        }
    }

    fn execute(&self, context: &mut Context, request: RoundRequest) -> HandlerResult<StepDecision> {
        if let Some(previous_round) = &request.previous_round {
            let late_results = BRANCH_RESULTS.remaining_branch_results(context, previous_round)?;
            LATE_RESULTS.set(context, late_results)?;
        }
        let round_number = request.round_number;
        let labels = ["a", "b", "c"].map(|branch| format!("round-{round_number}-{branch}"));
        let branch_builders = labels
            .into_iter()
            .map(|label| {
                Box::new(move |round: &JoinRound| {
                    StepMovement::to(
                        &ReleasedBranch,
                        BranchInput {
                            label,
                            round: round.clone(),
                        },
                    )
                }) as BranchBuilder<'static>
            })
            .collect();
        let movements = BRANCH_RESULTS.fork(context, branch_builders, |round| {
            StepMovement::to(
                &PartialRoundJoin,
                PartialRoundJoinInput {
                    round_number,
                    round,
                },
            )
        })?;
        Ok(StepDecision::go_to_many(movements))
    }
}

pub(crate) struct PartialRoundJoin;

impl Step for PartialRoundJoin {
    type Input = PartialRoundJoinInput;

    fn wait_for(
        &self,
        _context: &mut Context,
        input: PartialRoundJoinInput,
    ) -> HandlerResult<Wait> {
        Ok(Wait::until(BRANCH_RESULTS.for_branches(
            &input.round,
            RequestRound::REQUIRED_RESULTS,
        )))
    }

    fn execute(
        &self,
        context: &mut Context,
        input: PartialRoundJoinInput,
    ) -> HandlerResult<StepDecision> {
        let mut results = BRANCH_RESULTS.branch_results(context, &input.round)?;
        results.sort();
        if input.round_number == 1 {
            FIRST_ROUND_RESULTS.set(context, results)?;
            return Ok(StepDecision::go_to(
                &RequestRound,
                RoundRequest {
                    round_number: 2,
                    previous_round: Some(input.round),
                },
            ));
        }
        // The last round's third branch never reports, so the Flow closes without a reset.
        Ok(StepDecision::force_complete(PartialJoinRoundsOutput {
            first_round_results: FIRST_ROUND_RESULTS.get(context)?.unwrap_or_default(),
            late_first_round_results: LATE_RESULTS.get(context)?.unwrap_or_default(),
            second_round_key: input.round.round_key,
            second_round_results: results,
        }))
    }
}

pub(crate) struct CancellingFirstJoin(pub(crate) Arc<JoinTestState>);

impl Step for CancellingFirstJoin {
    type Input = JoinRound;

    fn wait_for(&self, _context: &mut Context, round: JoinRound) -> HandlerResult<Wait> {
        Ok(Wait::until(BRANCH_RESULTS.for_first_branch(&round)))
    }

    fn execute(&self, context: &mut Context, round: JoinRound) -> HandlerResult<StepDecision> {
        let winners = BRANCH_RESULTS.branch_results(context, &round)?;
        // The test commits a second branch result before this decision cancels the siblings.
        if !self.0.cancelling_join_release.wait(Duration::from_secs(30)) {
            return Err(HandlerError::new(
                "JoinFailure",
                "cancelling join was not released",
            ));
        }
        let winner = winners.into_iter().next().unwrap_or_default();
        Ok(StepDecision::go_to(&HoldUntilFinished, winner).cancel_sibling_step(&ReleasedBranch))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_method_timeout(Duration::from_secs(40))
    }
}

pub(crate) struct HoldUntilFinished;

impl Step for HoldUntilFinished {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _output: String) -> HandlerResult<Wait> {
        Ok(Wait::until(FINISH.for_one()))
    }

    fn execute(&self, _context: &mut Context, output: String) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(output))
    }
}

pub(crate) struct ConditionProbe;

impl ConditionProbe {
    fn condition(case: ConditionProbeCase) -> dex_sdk::Condition {
        let round = JoinRound {
            round_key: "probe-round".to_string(),
            branch_count: 3,
        };
        match case {
            ConditionProbeCase::BranchesZero => BRANCH_RESULTS.for_branches(&round, 0),
            ConditionProbeCase::BranchesAboveCount => BRANCH_RESULTS.for_branches(&round, 4),
            ConditionProbeCase::FirstBranchOfZeroBranches => {
                BRANCH_RESULTS.for_first_branch(&JoinRound {
                    branch_count: 0,
                    ..round
                })
            }
            ConditionProbeCase::RemainingBranchesAboveCount => {
                BRANCH_RESULTS.for_remaining_branches(&round, 4)
            }
            ConditionProbeCase::RemainingBranchesNoneLeft => {
                BRANCH_RESULTS.for_remaining_branches(&round, 3)
            }
        }
    }
}

impl Step for ConditionProbe {
    type Input = ConditionProbeInput;

    fn wait_for(&self, _context: &mut Context, input: ConditionProbeInput) -> HandlerResult<Wait> {
        let case = input
            .remaining_cases
            .first()
            .copied()
            .ok_or_else(|| HandlerError::new("JoinFailure", "no condition probe case"))?;
        Ok(Wait::until(Self::condition(case)))
    }

    fn execute(
        &self,
        context: &mut Context,
        mut input: ConditionProbeInput,
    ) -> HandlerResult<StepDecision> {
        let case = input.remaining_cases.remove(0);
        if context.wait_for_method_failed() {
            input.failed_cases.push(case);
        }
        if input.remaining_cases.is_empty() {
            return Ok(StepDecision::graceful_complete(input.failed_cases));
        }
        Ok(StepDecision::go_to(&ConditionProbe, input))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_retry(RetryPolicy::new().maximum_attempts(1))
            .wait_for_failure(WaitForFailurePolicy::Proceed)
    }
}

pub(crate) struct DeadlineJoin;

impl Step for DeadlineJoin {
    type Input = JoinRound;

    fn wait_for(&self, _context: &mut Context, round: JoinRound) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            BRANCH_RESULTS.for_all_branches(&round),
            Timer::by_duration(Duration::from_secs(2)),
        ]))
    }

    fn execute(&self, context: &mut Context, round: JoinRound) -> HandlerResult<StepDecision> {
        // The unreleased branch is still active, so complete without waiting for it.
        Ok(StepDecision::force_complete(DeadlineOutput {
            has_timer_fired: context.has_timer_fired(0),
            results: BRANCH_RESULTS.branch_results(context, &round)?,
        }))
    }
}
