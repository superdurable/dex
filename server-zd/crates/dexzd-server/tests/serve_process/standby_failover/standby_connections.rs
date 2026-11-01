// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Client connections across standby and serving phases, and calls in flight during a step down.

use std::future::Future;
use std::net::SocketAddr;
use std::pin::Pin;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::task::{Context, Poll};
use std::time::Duration;

use dexzd_protocol::dex::admin_service_client::AdminServiceClient;
use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::step_down_active_node_response::LeaseHolderAfterStepDown;
use dexzd_protocol::dex::{
    DescribeActiveNodeRequest, GetFlowSummaryRequest, SearchFlowsRequest, StepDownActiveNodeRequest,
};
use hyper_util::rt::TokioIo;
use tokio::net::TcpStream;
use tokio::task::JoinSet;
use tokio::time::Instant;
use tokio_util::sync::CancellationToken;
use tonic::Code;
use tonic::codegen::Service;
use tonic::codegen::http::Uri;
use tonic::transport::{Channel, Endpoint};

use super::held_calls::{HELD_CALL_START_WINDOW, HeldCall, start_flow_with_hard_delete_attribute};
use super::object_store_endpoints::ObjectStoreFault;
use super::{
    ACKNOWLEDGEMENTS_PER_PHASE, FIRST, FailoverScenario, HELD_READ_DELAY, HELD_READ_FLOW_ID,
    HOT_SLOTS, HOT_TAKEOVER_AFTER_RELEASE_BOUND, SECOND, STANDBY_BUDGET, TAKEOVER_BUDGET,
};

/// A standby instance's connection age, 3 s, plus its 1 s grace and a margin for a loaded host.
const STANDBY_CONNECTION_REACH_BOUND: Duration = Duration::from_secs(8);
/// A retired instance's GOAWAY after the step down answer, a reconnect, and a loaded host.
const SWITCHOVER_REACH_BOUND: Duration = Duration::from_secs(5);
/// Two standby connection ages and their grace; a serving instance must keep the connection that long.
const SERVING_CONNECTION_HOLD: Duration = Duration::from_secs(8);
const CALL_RETRY_INTERVAL: Duration = Duration::from_millis(100);
/// Readers that keep public calls in flight at the active while it steps down.
const IN_FLIGHT_READERS: usize = 16;
/// Lets every reader start reading before the step down.
const READERS_WARM_UP: Duration = Duration::from_millis(500);
/// A workload counter Flow, which every active can read.
const READ_FLOW_ID: &str = "counter-0";
const SEARCH_PAGE_SIZE: i32 = 100;
/// What a hot standby logs when it cannot open its store, such as one a stepped-down period still holds.
const STANDBY_BOOTSTRAP_RETRY: &str = "the hot standby could not bootstrap; retrying";
/// A loaded active's release after a step down call and a hot takeover; well under the held read.
const STEP_DOWN_SERVED_BOUND: Duration = Duration::from_millis(2500);
/// The default `standby.stepDownHoldOff`.
const STEP_DOWN_HOLD_OFF: Duration = Duration::from_secs(5);
/// What a serving process answers to a step down that reached one of its standby instances.
const STANDBY_INSTANCE_REFUSAL: &str = "the call reached a standby connection that closes soon";
/// A refusal needs no lease read or write.
const STANDBY_INSTANCE_REFUSAL_BOUND: Duration = Duration::from_secs(1);
/// Well inside the 250 ms before a retired standby instance sends GOAWAY.
const TAKEOVER_POLL_INTERVAL: Duration = Duration::from_millis(5);

/// A client whose first connection lands on a standby reaches the active, and later its successor.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_client_connected_to_a_standby_reconnects_to_the_active() {
    let mut scenario = FailoverScenario::start("standby-connection", &HOT_SLOTS).await;
    let group = &mut scenario.group;
    group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    group.wait_for_hot_standby(SECOND, STANDBY_BUDGET).await;
    let dials = Arc::new(AtomicUsize::new(0));
    let mut client = FlowServiceClient::new(stale_endpoint_channel(
        group.api_address(SECOND),
        group.router_address(),
        dials.clone(),
    ));
    let refused = client
        .get_flow_summary(summary_request())
        .await
        .expect_err("a standby refuses public calls");
    assert_eq!(refused.code(), Code::Unavailable, "{refused}");
    let reached_active_after = wait_until_served(&mut client, STANDBY_CONNECTION_REACH_BOUND).await;
    let dials_at_the_active = dials.load(Ordering::SeqCst);
    assert_eq!(
        dials_at_the_active, 2,
        "one aged standby connection, then the active"
    );
    keep_calling(&mut client, SERVING_CONNECTION_HOLD).await;
    assert_eq!(
        dials.load(Ordering::SeqCst),
        dials_at_the_active,
        "a serving instance must not age its connections"
    );
    let stepped_down_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    let outcome = group
        .step_down_active(None, None)
        .await
        .expect("the active steps down");
    assert!(
        matches!(
            outcome.lease_holder_after_step_down,
            Some(LeaseHolderAfterStepDown::TakenOverByAnotherProcess(_))
        ),
        "{outcome:?}"
    );
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    let reached_successor_after = wait_until_served(&mut client, SWITCHOVER_REACH_BOUND).await;
    scenario
        .finish(
            "a_client_connected_to_a_standby_reconnects_to_the_active",
            stepped_down_at,
            format!(
                "reached the active {reached_active_after:?} after a standby refused it, and the successor {reached_successor_after:?} after the takeover; {} dials",
                dials.load(Ordering::SeqCst)
            ),
        )
        .await;
}

/// A step down while public calls run at the active still leaves it a hot standby.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn step_down_with_calls_in_flight_leaves_a_hot_standby() {
    let mut scenario = FailoverScenario::start("step-down-in-flight", &HOT_SLOTS).await;
    let group = &mut scenario.group;
    group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    start_flow_with_hard_delete_attribute(group.router_channel(), HELD_READ_FLOW_ID).await;
    let stop_reading = CancellationToken::new();
    let readers = start_readers(group.api_address(FIRST), &stop_reading);
    tokio::time::sleep(READERS_WARM_UP).await;
    group
        .object_store(FIRST)
        .inject(ObjectStoreFault::HardDeleteLatency(HELD_READ_DELAY))
        .await;
    // Open across the whole step down, it must delay neither the handover nor the reopened store.
    let held_read =
        HeldCall::read_hard_delete_attribute(group.api_address(FIRST), HELD_READ_FLOW_ID);
    tokio::time::sleep(HELD_CALL_START_WINDOW).await;
    let stepped_down_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    group
        .step_down_active(None, None)
        .await
        .expect("the active steps down");
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    let served_at = Instant::now();
    let served_after = served_at - stepped_down_at;
    let (held_answer, held_read_ended_at) = held_read.finish().await;
    assert!(
        served_after <= STEP_DOWN_SERVED_BOUND,
        "an open read held up the handover: served {served_after:?} after the step down"
    );
    assert!(
        held_read_ended_at > served_at,
        "the read must stay open until the takeover, or it proves nothing"
    );
    let refusal = held_answer.expect_err("a read that outlives the node lease is refused");
    assert_eq!(refusal.code(), Code::Unavailable, "{refusal}");
    let hot_after = group.wait_for_hot_standby(FIRST, STANDBY_BUDGET).await;
    group.object_store(FIRST).clear_faults().await;
    let log = group.process_log(FIRST);
    assert!(
        !log.contains(STANDBY_BOOTSTRAP_RETRY),
        "the standby found the stepped-down store still open:\n{log}"
    );
    stop_reading.cancel();
    let reads = collect_reads(readers).await;
    assert!(reads > 0, "the readers read before the step down");
    scenario
        .finish(
            "step_down_with_calls_in_flight_leaves_a_hot_standby",
            stepped_down_at,
            format!(
                "served {served_after:?} after the step down; hot again {hot_after:?} after the takeover; {reads} reads answered"
            ),
        )
        .await;
}

/// A step down on a retiring standby instance's connection right after a takeover gets an answer.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn step_down_through_a_retiring_standby_instance_is_answered() {
    let mut scenario = FailoverScenario::start("standby-instance-step-down", &HOT_SLOTS).await;
    // A step down that misses the window waits for the first process's hold-off.
    scenario.expect_takeovers_after_release_within(
        Duration::ZERO,
        STEP_DOWN_HOLD_OFF + HOT_TAKEOVER_AFTER_RELEASE_BOUND,
    );
    let group = &mut scenario.group;
    group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    group.wait_for_hot_standby(SECOND, STANDBY_BUDGET).await;
    let stepped_down_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    // Opened just before the step down, so the takeover comes well within its 3 s age.
    let standby_channel = Channel::from_shared(format!("http://{}", group.api_address(SECOND)))
        .expect("the slot address is a valid URI")
        .connect()
        .await
        .expect("the standby accepts a connection");
    let connected_at = Instant::now();
    let mut standby_admin = AdminServiceClient::new(standby_channel);
    let not_active = standby_admin
        .describe_active_node(DescribeActiveNodeRequest {})
        .await
        .expect_err("a standby describes no active node");
    assert_eq!(not_active.code(), Code::FailedPrecondition, "{not_active}");
    group.note_possible_release();
    // The first answer waits for a lease read; the retiring instance's window may close by then.
    let mut router_admin = AdminServiceClient::new(group.router_channel());
    let first_step_down = tokio::spawn(async move {
        router_admin
            .step_down_active_node(StepDownActiveNodeRequest::default())
            .await
    });
    // The lifecycle describes itself as soon as it serves, right after it retires the standby instance.
    let described_at = Instant::now();
    loop {
        match standby_admin
            .describe_active_node(DescribeActiveNodeRequest {})
            .await
        {
            Ok(_) => break,
            Err(status) => assert!(
                status.code() == Code::FailedPrecondition
                    && described_at.elapsed() < TAKEOVER_BUDGET,
                "the standby never took over: {status}"
            ),
        }
        tokio::time::sleep(TAKEOVER_POLL_INTERVAL).await;
    }
    let sent_at = Instant::now();
    let connected_for = sent_at - connected_at;
    let answer = standby_admin
        .step_down_active_node(StepDownActiveNodeRequest::default())
        .await;
    let answered_after = sent_at.elapsed();
    first_step_down
        .await
        .expect("the first step down task does not panic")
        .expect("the first active steps down");
    // A call that misses the 250 ms window steps down through the serving instance instead.
    let path = match answer {
        Err(status)
            if status.code() == Code::FailedPrecondition
                && status.message().contains(STANDBY_INSTANCE_REFUSAL) =>
        {
            assert!(
                answered_after <= STANDBY_INSTANCE_REFUSAL_BOUND,
                "the refusal took {answered_after:?}"
            );
            assert_eq!(
                group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
                SECOND,
                "the refused step down changed nothing"
            );
            "refused on the retiring standby instance"
        }
        Ok(outcome) => {
            assert!(
                matches!(
                    outcome.get_ref().lease_holder_after_step_down,
                    Some(LeaseHolderAfterStepDown::TakenOverByAnotherProcess(_))
                ),
                "{outcome:?}"
            );
            assert_eq!(
                group.wait_for_takeover(SECOND, TAKEOVER_BUDGET).await,
                FIRST
            );
            "stepped down through the serving instance"
        }
        Err(status) => panic!("the step down answer was lost after {answered_after:?}: {status}"),
    };
    scenario
        .finish(
            "step_down_through_a_retiring_standby_instance_is_answered",
            stepped_down_at,
            format!(
                "the step down {path} after {answered_after:?}, sent {connected_for:?} after the connection opened"
            ),
        )
        .await;
}

/// Dials `first` once, like a client handed a stale endpoint, then `later` on every reconnect.
#[derive(Clone)]
struct StaleEndpointConnector {
    first: SocketAddr,
    later: SocketAddr,
    dials: Arc<AtomicUsize>,
}

impl Service<Uri> for StaleEndpointConnector {
    type Response = TokioIo<TcpStream>;
    type Error = std::io::Error;
    type Future = Pin<Box<dyn Future<Output = Result<Self::Response, Self::Error>> + Send>>;

    fn poll_ready(&mut self, _context: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        Poll::Ready(Ok(()))
    }

    fn call(&mut self, _uri: Uri) -> Self::Future {
        let address = if self.dials.fetch_add(1, Ordering::SeqCst) == 0 {
            self.first
        } else {
            self.later
        };
        Box::pin(async move { TcpStream::connect(address).await.map(TokioIo::new) })
    }
}

fn stale_endpoint_channel(
    first: SocketAddr,
    later: SocketAddr,
    dials: Arc<AtomicUsize>,
) -> Channel {
    Endpoint::from_static("http://stale-endpoint.test").connect_with_connector_lazy(
        StaleEndpointConnector {
            first,
            later,
            dials,
        },
    )
}

/// Retries a read until an active answers it; returns how long that took.
async fn wait_until_served(client: &mut FlowServiceClient<Channel>, bound: Duration) -> Duration {
    let started = Instant::now();
    loop {
        match client.get_flow_summary(summary_request()).await {
            Ok(_) => return started.elapsed(),
            Err(status) => assert!(
                started.elapsed() < bound,
                "no active answered within {bound:?}; last answer: {status}"
            ),
        }
        tokio::time::sleep(CALL_RETRY_INTERVAL).await;
    }
}

/// Reads for `duration`; every read must succeed on the current connection.
async fn keep_calling(client: &mut FlowServiceClient<Channel>, duration: Duration) {
    let started = Instant::now();
    while started.elapsed() < duration {
        client
            .get_flow_summary(summary_request())
            .await
            .expect("the active keeps answering");
        tokio::time::sleep(CALL_RETRY_INTERVAL).await;
    }
}

/// Starts readers straight to `address`: summaries, and searches that hold a store view; each counts answers.
fn start_readers(address: SocketAddr, stop_reading: &CancellationToken) -> JoinSet<usize> {
    let mut readers = JoinSet::new();
    for _ in 0..IN_FLIGHT_READERS {
        let stop_reading = stop_reading.clone();
        readers.spawn(async move {
            let channel = Channel::from_shared(format!("http://{address}"))
                .expect("the slot address is a valid URI")
                .connect_lazy();
            let mut client = FlowServiceClient::new(channel);
            let mut summary_client = client.clone();
            let mut answered = 0;
            while !stop_reading.is_cancelled() {
                let (summary, search) = tokio::join!(
                    summary_client.get_flow_summary(summary_request()),
                    client.search_flows(SearchFlowsRequest {
                        page_size: SEARCH_PAGE_SIZE,
                        ..SearchFlowsRequest::default()
                    }),
                );
                // Reads fail once the active steps down; only answered ones count.
                if summary.is_ok() && search.is_ok() {
                    answered += 1;
                } else {
                    tokio::time::sleep(CALL_RETRY_INTERVAL).await;
                }
            }
            answered
        });
    }
    readers
}

async fn collect_reads(mut readers: JoinSet<usize>) -> usize {
    let mut reads = 0;
    while let Some(answered) = readers.join_next().await {
        reads += answered.expect("a reader does not panic");
    }
    reads
}

fn summary_request() -> GetFlowSummaryRequest {
    GetFlowSummaryRequest {
        flow_id: READ_FLOW_ID.to_owned(),
        run_id: String::new(),
    }
}
