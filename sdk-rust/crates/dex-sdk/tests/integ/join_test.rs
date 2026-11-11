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
    ChannelMap, Client, Flow, Join, JoinRound, PersistenceSchema, Registry, StepExecutionId, Value,
    WaitForStepCompletionOptions,
};

use crate::join_workflow::{
    BranchReport, CancellingFirstJoin, ConditionProbeCase, DeadlineOutput, JoinGate, JoinOutput,
    JoinScenario, JoinTestState, JoinWorkflow, PartialJoinRoundsOutput, PartialRoundJoin,
    ReleasedBranch, START_ROUND_KEY,
};
use crate::support::{DexDevTestEnvironment, flow_id};

static DUPLICATE_JOIN: LazyLock<Join<String>> = LazyLock::new(|| Join::new("rust-join-duplicate"));
static DUPLICATE_CHANNEL_MAP: LazyLock<ChannelMap<String>> =
    LazyLock::new(|| ChannelMap::new("rust-join-duplicate"));

struct JoinScenarioRun {
    state: Arc<JoinTestState>,
    environment: DexDevTestEnvironment,
    flow_id: String,
}

impl JoinScenarioRun {
    fn start(scenario: JoinScenario, flow_id_prefix: &str) -> Self {
        let state = Arc::new(JoinTestState::default());
        let environment = DexDevTestEnvironment::start(
            Registry::new().register(JoinWorkflow::new(Arc::clone(&state))),
        );
        let flow_id = flow_id(flow_id_prefix);
        environment
            .client
            .start_flow(&JoinWorkflow::new(Arc::clone(&state)), &flow_id, scenario)
            .expect("start Join Flow");
        Self {
            state,
            environment,
            flow_id,
        }
    }

    fn client(&self) -> &Client {
        &self.environment.client
    }

    fn release_branch(&self, label: &str) {
        self.client()
            .invoke_rpc(
                &self.flow_id,
                JoinWorkflow::RELEASE_BRANCH,
                label.to_string(),
            )
            .expect("release Join branch");
    }

    fn wait_for_step(&self, step_execution: StepExecutionId, request_suffix: &str) {
        self.client()
            .wait_for_step_completion(
                &self.flow_id,
                step_execution,
                WaitForStepCompletionOptions::new()
                    .request_id(format!("{}-{request_suffix}", self.flow_id))
                    .request_timeout(Duration::from_secs(30)),
            )
            .expect("wait for Step completion");
    }

    fn wait_for_released_branch(&self, execution_number: u32) {
        self.wait_for_step(
            StepExecutionId::of(&ReleasedBranch).execution_number(execution_number),
            &format!("branch-{execution_number}"),
        );
    }

    fn pending_results(&self, round_key: &str) -> Vec<String> {
        self.client()
            .invoke_rpc(
                &self.flow_id,
                JoinWorkflow::GET_PENDING_RESULTS,
                round_key.to_string(),
            )
            .expect("read pending Join results")
    }

    fn output<Output: Value>(&self) -> Output {
        self.client()
            .wait_for_flow_with_timeout(&self.flow_id, Duration::from_secs(30))
            .and_then(|result| result.single_output())
            .expect("complete Join Flow")
    }
}

fn start_round(branch_count: usize) -> JoinRound {
    JoinRound {
        round_key: START_ROUND_KEY.to_string(),
        branch_count,
    }
}

fn sorted(mut values: Vec<String>) -> Vec<String> {
    values.sort();
    values
}

fn strings(values: &[&str]) -> Vec<String> {
    values.iter().map(|value| value.to_string()).collect()
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_join_all_branches_reported_before_join_waits() {
    let run = JoinScenarioRun::start(JoinScenario::AllOfBeforeWait, "rust-join-all-of");
    for (label, execution_number) in [("C", 3), ("A", 1), ("B", 2)] {
        run.release_branch(label);
        run.wait_for_released_branch(execution_number);
    }
    // The join Step starts only now, after every result committed.
    run.release_branch(JoinGate::GATE_LABEL);

    assert_eq!(
        JoinOutput {
            round_key: START_ROUND_KEY.to_string(),
            branch_count: 3,
            results: strings(&["C", "A", "B"]),
        },
        run.output::<JoinOutput>()
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_join_multiple_instances_count_the_input_list() {
    let reviewers = strings(&["reviewer-1", "reviewer-2", "reviewer-3", "reviewer-4"]);
    let run = JoinScenarioRun::start(
        JoinScenario::MultipleInstances(reviewers.clone()),
        "rust-join-multiple-instances",
    );
    for reviewer in &reviewers {
        run.release_branch(reviewer);
    }

    let output = run.output::<JoinOutput>();
    assert_eq!(4, output.branch_count);
    assert_eq!(reviewers, sorted(output.results));
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_join_partial_rounds_reset_before_the_next_round() {
    let run = JoinScenarioRun::start(JoinScenario::PartialJoinRounds, "rust-join-partial-rounds");
    run.release_branch("round-1-a");
    run.release_branch("round-1-b");
    run.wait_for_step(StepExecutionId::of(&PartialRoundJoin), "first-join");
    // Round 2 enablement arrives before the reset; its branches do not exist yet.
    run.release_branch("round-2-a");
    run.release_branch("round-2-b");
    run.release_branch("round-1-c");

    assert_eq!(
        PartialJoinRoundsOutput {
            first_round_results: strings(&["round-1-a", "round-1-b"]),
            late_first_round_results: strings(&["round-1-c"]),
            second_round_key: "RequestRound-2".to_string(),
            second_round_results: strings(&["round-2-a", "round-2-b"]),
        },
        run.output::<PartialJoinRoundsOutput>()
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_join_ignores_results_of_another_round_key() {
    let run = JoinScenarioRun::start(JoinScenario::KeyIsolation, "rust-join-key-isolation");
    let other_round = JoinRound {
        round_key: "other-round".to_string(),
        branch_count: 2,
    };
    for result in ["intruder-1", "intruder-2"] {
        run.client()
            .invoke_rpc(
                &run.flow_id,
                JoinWorkflow::REPORT_BRANCH_RESULT,
                BranchReport {
                    round: other_round.clone(),
                    result: result.to_string(),
                },
            )
            .expect("report to another round");
    }
    assert_eq!(
        strings(&["intruder-1", "intruder-2"]),
        run.pending_results("other-round")
    );
    run.release_branch("isolated-a");
    run.release_branch("isolated-b");

    let output = run.output::<JoinOutput>();
    assert_eq!(
        strings(&["isolated-a", "isolated-b"]),
        sorted(output.results)
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_join_first_branch_cancels_sibling_branches() {
    let run = JoinScenarioRun::start(
        JoinScenario::CancellingFirstBranch,
        "rust-join-cancelling-first",
    );
    run.release_branch("first-a");
    run.wait_for_released_branch(1);
    run.release_branch("first-b");
    run.wait_for_released_branch(2);
    run.state.cancelling_join_release.set();
    run.wait_for_step(
        StepExecutionId::of(&CancellingFirstJoin(Arc::clone(&run.state))),
        "cancelling-join",
    );

    // first-b committed after the join matched, so its result stays pending.
    assert_eq!(strings(&["first-b"]), run.pending_results(START_ROUND_KEY));
    // first-c was canceled; releasing it now reports nothing.
    run.release_branch("first-c");
    assert_eq!(strings(&["first-b"]), run.pending_results(START_ROUND_KEY));
    run.client()
        .invoke_rpc_without_input(&run.flow_id, JoinWorkflow::FINISH_FLOW)
        .expect("finish Flow");

    assert_eq!("first-a", run.output::<String>());
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_join_rpc_reports_for_a_canceled_branch() {
    let run = JoinScenarioRun::start(
        JoinScenario::RpcReportsCanceledBranch,
        "rust-join-rpc-reports-canceled",
    );
    run.release_branch("reported-a");
    run.wait_for_released_branch(1);
    run.client()
        .invoke_rpc(
            &run.flow_id,
            JoinWorkflow::CANCEL_BRANCH_AND_REPORT_RESULT,
            BranchReport {
                round: start_round(2),
                result: "reported-b-canceled".to_string(),
            },
        )
        .expect("cancel branch and report its result");

    let output = run.output::<JoinOutput>();
    assert_eq!(
        strings(&["reported-a", "reported-b-canceled"]),
        output.results
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_join_second_fork_in_one_invocation_fails_the_step() {
    let run = JoinScenarioRun::start(JoinScenario::DoubleFork, "rust-join-double-fork");

    let failure = run.output::<String>();
    assert!(
        failure.contains("Join rust-join-branch-results already forked in this invocation"),
        "{failure}"
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_join_rpc_forks_only_with_an_explicit_round_key() {
    let run = JoinScenarioRun::start(JoinScenario::RpcFork, "rust-join-rpc-fork");
    let fork_output = run
        .client()
        .invoke_rpc(
            &run.flow_id,
            JoinWorkflow::FORK_JOIN_ROUND,
            "rpc-round-1".to_string(),
        )
        .expect("fork from an RPC");
    assert!(
        fork_output
            .fork_error
            .contains("requires a Step execution ID"),
        "{}",
        fork_output.fork_error
    );
    assert!(
        fork_output
            .slash_key_error
            .contains("map instances must not contain '/'"),
        "{}",
        fork_output.slash_key_error
    );
    run.release_branch("rpc-a");
    run.release_branch("rpc-b");

    let output = run.output::<JoinOutput>();
    assert_eq!("rpc-round-1", output.round_key);
    assert_eq!(2, output.branch_count);
    assert_eq!(strings(&["rpc-a", "rpc-b"]), sorted(output.results));
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_join_invalid_counts_fail_the_wait() {
    let run = JoinScenarioRun::start(
        JoinScenario::InvalidConditions,
        "rust-join-invalid-conditions",
    );

    // received_count and count are usize, so negative counts cannot be expressed.
    assert_eq!(
        vec![
            ConditionProbeCase::BranchesZero,
            ConditionProbeCase::BranchesAboveCount,
            ConditionProbeCase::FirstBranchOfZeroBranches,
            ConditionProbeCase::RemainingBranchesAboveCount,
        ],
        run.output::<Vec<ConditionProbeCase>>()
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_join_zero_branches_complete_immediately() {
    let run = JoinScenarioRun::start(JoinScenario::ZeroBranches, "rust-join-zero-branches");

    assert_eq!(
        JoinOutput {
            round_key: START_ROUND_KEY.to_string(),
            branch_count: 0,
            results: Vec::new(),
        },
        run.output::<JoinOutput>()
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_join_timer_deadline_wins_over_a_missing_branch() {
    let run = JoinScenarioRun::start(JoinScenario::TimerDeadline, "rust-join-timer-deadline");
    run.release_branch("deadline-a");

    assert_eq!(
        DeadlineOutput {
            has_timer_fired: true,
            results: Vec::new(),
        },
        run.output::<DeadlineOutput>()
    );
}

struct DuplicateJoinFlow;

impl Flow for DuplicateJoinFlow {
    type StartInput = ();

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .join(&DUPLICATE_JOIN)
            .channel_map(&DUPLICATE_CHANNEL_MAP)
    }
}

#[test]
fn test_join_name_conflicts_with_a_channel_map() {
    let error = Registry::new()
        .register(DuplicateJoinFlow)
        .err()
        .expect("duplicate Join name fails registration");

    assert!(
        error
            .to_string()
            .contains("duplicate persistence definition rust-join-duplicate"),
        "{error}"
    );
}
