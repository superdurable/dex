// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! P3: a frozen active loses the node; once resumed, it answers no stale read and no SERVING.

use std::net::SocketAddr;
use std::time::Duration;

use dexzd_protocol::dex::GetFlowSummaryRequest;
use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use tokio::task::JoinSet;
use tokio::time::Instant;
use tonic::Code;
use tonic::transport::Channel;
use tonic_health::pb::HealthCheckRequest;
use tonic_health::pb::health_check_response::ServingStatus;
use tonic_health::pb::health_client::HealthClient;

use super::node_group::{DirectoryFate, ProcessSignal, SlotSettings, SlotStandbyMode};
use super::{
    ACKNOWLEDGEMENTS_PER_PHASE, FIRST, FailoverScenario, HOT_SLOTS, SECOND, SLOTS, STANDBY_BUDGET,
    TAKEOVER_BUDGET, assert_exited_after_losing_the_lease,
};

/// A process that lost its lease exits within the lease and the watchdog's abort budget.
const LOST_LEASE_EXIT_BUDGET: Duration = Duration::from_secs(20);
/// Health probes queued at a frozen process, answered once it resumes.
const BACKLOGGED_PROBES: usize = 4;
const BACKLOGGED_PROBE_BUDGET: Duration = Duration::from_secs(10);
const BACKLOG_SEND_WINDOW: Duration = Duration::from_millis(200);
/// Connections used before the freeze, so their reads queue at the frozen process.
const QUEUED_READ_CONNECTIONS: usize = 6;
/// Long enough to fire after the old active resumed, short enough for the final verification.
const NEW_ACTIVE_TIMER: Duration = Duration::from_secs(1);
/// What a public call answers while the node lease is not valid.
const LEASE_NOT_VALID_MESSAGE: &str = "the node lease is not valid";

/// P3, hot: a frozen active loses the node to the hot standby, exits on resume; records queued health answers.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn frozen_active_loses_the_node_to_the_hot_standby_and_exits_on_resume() {
    freeze_the_active_through_a_takeover(
        "frozen_active_loses_the_node_to_the_hot_standby_and_exits_on_resume",
        "hot-frozen",
        &HOT_SLOTS,
    )
    .await;
}

/// P3, cold: a frozen active loses the node to the cold standby and answers nothing stale on resume.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn frozen_active_loses_the_node_to_the_cold_standby_and_exits_on_resume() {
    freeze_the_active_through_a_takeover(
        "frozen_active_loses_the_node_to_the_cold_standby_and_exits_on_resume",
        "cold-frozen",
        &SLOTS,
    )
    .await;
}

/// How one read queued at the frozen old active ended.
#[derive(Debug, PartialEq, Eq)]
enum QueuedRead {
    /// The read returned a summary or NOT_FOUND; the old active cannot know the new Flow.
    Answered(Code),
    /// The read hit the lease gate.
    LeaseNotValid,
    /// The read failed another way, such as a closed connection or a timeout.
    Failed(String),
}

/// Freezes the active until the standby serves and acknowledges a new Flow, then resumes it.
async fn freeze_the_active_through_a_takeover(test: &str, phase: &str, slots: &[SlotSettings<'_>]) {
    let mut scenario = FailoverScenario::start(phase, slots).await;
    scenario
        .group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    let old_active_clients = connect_flow_clients(scenario.group.api_address(FIRST)).await;
    let frozen_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    group.signal(FIRST, ProcessSignal::Stop).await;
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    let served_after = frozen_at.elapsed();
    group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    let new_flow_id = scenario
        .workload
        .start_timer_flow(scenario.group.router_channel(), NEW_ACTIVE_TIMER)
        .await;
    let group = &mut scenario.group;
    let queued_reads = queue_reads(old_active_clients, &new_flow_id);
    let backlog = queue_health_probes(group.api_address(FIRST));
    // Lets the probes reach the frozen process's socket before it resumes.
    tokio::time::sleep(BACKLOG_SEND_WINDOW).await;
    group.permit_exit(FIRST);
    let resumed_at = Instant::now();
    group.signal(FIRST, ProcessSignal::Continue).await;
    let read_answers = collect_reads(queued_reads).await;
    let backlogged_answers = collect_health_answers(backlog, resumed_at).await;
    assert!(
        read_answers
            .iter()
            .all(|answer| !matches!(answer, QueuedRead::Answered(_)))
            && read_answers.contains(&QueuedRead::LeaseNotValid),
        "the resumed old active must refuse every queued read for the lease: {read_answers:?}"
    );
    assert!(
        backlogged_answers
            .iter()
            .all(|(status, _)| *status != ServingStatus::Serving),
        "the resumed old active must not answer SERVING: {backlogged_answers:?}"
    );
    let status = group
        .wait_for_permitted_exit(FIRST, LOST_LEASE_EXIT_BUDGET)
        .await;
    assert_exited_after_losing_the_lease(status);
    group.restart(FIRST, DirectoryFate::Keep);
    if slots[FIRST].standby_mode == SlotStandbyMode::Hot {
        group.wait_for_hot_standby(FIRST, STANDBY_BUDGET).await;
    } else {
        group.wait_for_standby(FIRST, STANDBY_BUDGET).await;
    }
    scenario
        .finish(
            test,
            frozen_at,
            format!(
                "served {served_after:?} after the freeze; queued reads after the resume: {read_answers:?}; queued health answers after the resume: {backlogged_answers:?}; old active ended with {status}"
            ),
        )
        .await;
}

/// Connects to `address` and reads once on each connection, so later reads reuse it.
async fn connect_flow_clients(address: SocketAddr) -> Vec<FlowServiceClient<Channel>> {
    let mut clients = Vec::new();
    for _ in 0..QUEUED_READ_CONNECTIONS {
        let channel = Channel::from_shared(format!("http://{address}"))
            .expect("the slot address is a valid URI")
            .connect()
            .await
            .expect("the active accepts a connection");
        let mut client = FlowServiceClient::new(channel);
        let warm_up = client
            .get_flow_summary(summary_request("not-started"))
            .await;
        assert!(
            warm_up.is_ok()
                || warm_up
                    .as_ref()
                    .is_err_and(|status| status.code() == Code::NotFound),
            "the active answers a read: {warm_up:?}"
        );
        clients.push(client);
    }
    clients
}

/// Sends one summary read of `flow_id` on each client; the frozen process queues them.
fn queue_reads(clients: Vec<FlowServiceClient<Channel>>, flow_id: &str) -> JoinSet<QueuedRead> {
    let mut reads = JoinSet::new();
    for mut client in clients {
        let request = summary_request(flow_id);
        reads.spawn(async move {
            let answer =
                tokio::time::timeout(BACKLOGGED_PROBE_BUDGET, client.get_flow_summary(request))
                    .await;
            match answer {
                Ok(Ok(_)) => QueuedRead::Answered(Code::Ok),
                Ok(Err(status))
                    if status.code() == Code::Unavailable
                        && status.message() == LEASE_NOT_VALID_MESSAGE =>
                {
                    QueuedRead::LeaseNotValid
                }
                Ok(Err(status)) if status.code() == Code::NotFound => {
                    QueuedRead::Answered(Code::NotFound)
                }
                Ok(Err(status)) => QueuedRead::Failed(status.to_string()),
                Err(_) => QueuedRead::Failed("no answer in time".to_owned()),
            }
        });
    }
    reads
}

fn summary_request(flow_id: &str) -> GetFlowSummaryRequest {
    GetFlowSummaryRequest {
        flow_id: flow_id.to_owned(),
        run_id: String::new(),
    }
}

async fn collect_reads(mut reads: JoinSet<QueuedRead>) -> Vec<QueuedRead> {
    let mut answers = Vec::new();
    while let Some(answer) = reads.join_next().await {
        answers.push(answer.expect("a read task does not panic"));
    }
    answers
}

/// Sends health checks to a frozen process; each waits for the process to resume.
fn queue_health_probes(address: SocketAddr) -> JoinSet<(ServingStatus, Instant)> {
    let mut probes = JoinSet::new();
    for _ in 0..BACKLOGGED_PROBES {
        probes.spawn(async move {
            let channel = Channel::from_shared(format!("http://{address}"))
                .expect("the slot address is a valid URI")
                .connect_lazy();
            let answer = tokio::time::timeout(
                BACKLOGGED_PROBE_BUDGET,
                HealthClient::new(channel).check(HealthCheckRequest {
                    service: String::new(),
                }),
            )
            .await;
            let status = match answer {
                Ok(Ok(response)) => ServingStatus::try_from(response.get_ref().status)
                    .unwrap_or(ServingStatus::Unknown),
                Ok(Err(_)) | Err(_) => ServingStatus::Unknown,
            };
            (status, Instant::now())
        });
    }
    probes
}

/// Returns each queued probe's answer and how long after `resumed_at` it arrived.
async fn collect_health_answers(
    mut probes: JoinSet<(ServingStatus, Instant)>,
    resumed_at: Instant,
) -> Vec<(ServingStatus, Duration)> {
    let mut answers = Vec::new();
    while let Some(answer) = probes.join_next().await {
        let (status, answered_at) = answer.expect("a health probe task does not panic");
        answers.push((status, answered_at.saturating_duration_since(resumed_at)));
    }
    answers
}
