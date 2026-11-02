// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! One process's life: wait for the lease, serve, step down, and wait again.

mod active_node_requests;
mod lease_holder_id_source;
#[cfg(test)]
mod local_store_close_tests;
mod standby_status;

pub(super) use active_node_requests::{
    AdministrationRequest, HeldNodeLease, LifecycleActiveNodeAdministration, StepDownRequest,
};
pub(super) use lease_holder_id_source::LeaseHolderRecordingIdSource;

use std::sync::Arc;
use std::time::Duration;

use dexzd_clock::{SystemWallClock, WallClock};
use dexzd_cluster_metadata::{LeaseValidityDeadline, read_node_lease};
use dexzd_engine::{
    CommandApplier, DeterministicCommandApplier, HotStandby, HotStandbyConfig,
    HotStandbyDependencies, LocalStoreCloseWaiter,
};
use dexzd_metrics::{NodeMetrics, NodeRole, TakeoverStandbyMode};
use tokio::sync::{mpsc, oneshot, watch};
use tokio::task::JoinHandle;
use tokio::time::Instant;
use tokio_util::sync::CancellationToken;

use self::active_node_requests::PendingStepDownAnswer;
use self::standby_status::{StandbyAcquisitionDelay, StandbyStatusPublisher};
use super::bounded_teardown::BoundedTeardown;
use super::grpc_server::{GrpcInstancePhase, GrpcServer, GrpcSettings};
use super::{
    EngineStart, GRPC_DRAIN_BUDGET, PeriodEnd, PeriodStopKind, ServerNode, ServerNodeStart,
    embedded_s3_stop_budget, until_cancelled, wait_for_cancellation,
};
use crate::ServerError;
use crate::component_configs::ComponentConfigs;
use crate::config::Config;
use crate::embedded_s3::EmbeddedS3;
use crate::metrics_server::MetricsServer;
use crate::object_stores::ObjectStores;

/// How often a process that stepped down reads the lease to name its successor.
const SUCCESSOR_POLL_INTERVAL: Duration = Duration::from_millis(250);
/// Bounds one such read, so a slow object store never holds up a shutdown.
const SUCCESSOR_READ_TIMEOUT: Duration = Duration::from_secs(1);
/// First wait before a takeover retries after an error before the lease; later waits double.
const TAKEOVER_RETRY_INITIAL_BACKOFF: Duration = Duration::from_secs(1);
const TAKEOVER_RETRY_MAX_BACKOFF: Duration = Duration::from_secs(30);
/// Bounds the wait for a stepped-down period's local store to close before this process reopens it.
/// Twice the 30 s object storage request timeout, so a task stuck in one request lets go first.
const LOCAL_STORE_CLOSE_BUDGET: Duration = Duration::from_secs(60);

/// Everything one process keeps across serving periods.
pub(super) struct ProcessParts {
    pub(super) config: Config,
    pub(super) components: ComponentConfigs,
    pub(super) metrics: Arc<NodeMetrics>,
    pub(super) lease_validity_deadline: LeaseValidityDeadline,
    pub(super) object_stores: ObjectStores,
    pub(super) clock: Arc<dyn WallClock>,
    pub(super) applier: Arc<dyn CommandApplier>,
    pub(super) id_source: Arc<LeaseHolderRecordingIdSource>,
    /// Spreads this process's hot standby restarts apart from other processes'.
    pub(super) restart_jitter_seed: u64,
}

/// How a process enters standby: the hold-off after a step down and the answer it still owes.
#[derive(Default)]
struct StandbyEntry {
    hold_off_until: Option<Instant>,
    pending: Option<PendingStepDownAnswer>,
}

/// How the wait for a stepped-down period's local store to close ended.
enum LocalStoreCloseOutcome {
    Closed,
    /// A handle still held the store after [`LOCAL_STORE_CLOSE_BUDGET`].
    StillOpen,
    Cancelled,
}

/// The serving process, which steps down to standby on request and serves again later.
pub(super) struct NodeLifecycle {
    process: ProcessParts,
    grpc_settings: GrpcSettings,
    grpc: GrpcServer,
    requests: mpsc::Receiver<AdministrationRequest>,
    node: ServerNode,
    /// Earlier gRPC instances that finish their open calls in the background.
    retirements: Vec<JoinHandle<()>>,
}

impl NodeLifecycle {
    /// Connects object storage, waits for the lease, and starts serving.
    ///
    /// Returns `None` when `cancellation` arrives first; the gRPC server is then stopped.
    #[expect(
        clippy::too_many_arguments,
        reason = "the process parts move in once, so a parameter struct would only rename them"
    )]
    pub(super) async fn start(
        config: &Config,
        components: ComponentConfigs,
        metrics: Arc<NodeMetrics>,
        lease_validity_deadline: LeaseValidityDeadline,
        grpc_settings: GrpcSettings,
        grpc: GrpcServer,
        mut requests: mpsc::Receiver<AdministrationRequest>,
        cancellation: &mut watch::Receiver<bool>,
    ) -> Result<Option<Self>, ServerError> {
        // A cancelled startup check may leave its probe object behind.
        let connected = until_cancelled(
            ObjectStores::connect(
                &config.object_store,
                config.commit.multipart_threshold_bytes,
                metrics.as_ref(),
            ),
            cancellation,
        )
        .await;
        let object_stores = match connected {
            Some(Ok(object_stores)) => object_stores,
            Some(Err(error)) => {
                grpc.abort().await;
                return Err(error);
            }
            None => {
                grpc.abort().await;
                return Ok(None);
            }
        };
        let process = ProcessParts {
            config: config.clone(),
            components,
            metrics,
            lease_validity_deadline,
            object_stores,
            clock: Arc::new(SystemWallClock),
            applier: Arc::new(DeterministicCommandApplier),
            id_source: Arc::new(LeaseHolderRecordingIdSource::default()),
            restart_jitter_seed: rand::random(),
        };
        let started = acquire_serving_node(
            &process,
            &grpc_settings,
            &mut requests,
            StandbyEntry::default(),
            cancellation,
        )
        .await;
        match started {
            Ok(Some(mut node)) => {
                let mut grpc = grpc;
                node.activate_public_services(&grpc);
                let retirement = tokio::spawn(swap_in_serving_instance(&mut grpc, &grpc_settings));
                Ok(Some(Self {
                    process,
                    grpc_settings,
                    grpc,
                    requests,
                    node,
                    retirements: vec![retirement],
                }))
            }
            Ok(None) => {
                grpc.abort().await;
                Ok(None)
            }
            Err(error) => {
                grpc.abort().await;
                Err(error)
            }
        }
    }

    /// Serves until shutdown or a failure, stepping down and serving again on request.
    pub(super) async fn run(
        self,
        metrics_server: Option<MetricsServer>,
        shutdown_request: CancellationToken,
        embedded_s3: Option<EmbeddedS3>,
    ) -> Result<(), ServerError> {
        let Self {
            process,
            grpc_settings,
            mut grpc,
            mut requests,
            mut node,
            mut retirements,
        } = self;
        let (cancellation_sender, mut cancellation) = watch::channel(false);
        let shutdown_watch = shutdown_request.clone();
        let forward_shutdown = tokio::spawn(async move {
            shutdown_watch.cancelled().await;
            cancellation_sender.send_replace(true);
        });
        let (outcome, mut teardown) = loop {
            let end = node
                .serve_until_stopped(&mut grpc, &shutdown_request, &mut requests)
                .await;
            let request = match end {
                PeriodEnd::StepDown(request) => request,
                PeriodEnd::Shutdown => {
                    tracing::info!("dex-server-zd shutting down");
                    let stopped = node.stop(&mut grpc, PeriodStopKind::Shutdown).await;
                    break (Ok(()), stopped.teardown);
                }
                PeriodEnd::Failed(error) => {
                    tracing::error!(%error, "dex-server-zd stopping after a failure");
                    let stopped = node.stop(&mut grpc, PeriodStopKind::Shutdown).await;
                    break (Err(error), stopped.teardown);
                }
            };
            let released = node.held_lease().clone();
            tracing::info!(
                node_epoch = released.node_epoch,
                "stepping down: stopping serving and releasing the node lease"
            );
            let stopped = node.stop(&mut grpc, PeriodStopKind::StepDown).await;
            // The old instance keeps the step down call's connection until the answer goes out.
            let (answered, answer_sent) = oneshot::channel();
            let retiring = std::mem::replace(
                &mut grpc,
                GrpcServer::start(&grpc_settings, GrpcInstancePhase::Standby),
            );
            prune_finished_retirements(&mut retirements).await;
            let retirement = tokio::spawn(retiring.retire_after(answer_sent, GRPC_DRAIN_BUDGET));
            retirements.push(retirement);
            let teardown_result = stopped.teardown.finish(Ok(()));
            if let Err(error) = &teardown_result {
                tracing::warn!(%error, "stepping down left steps unfinished; standing by anyway");
            }
            let hold_off = process.components.standby.step_down_hold_off;
            let pending = if stopped.has_released_lease {
                Some(PendingStepDownAnswer::new(
                    request, &released, hold_off, answered,
                ))
            } else {
                // Serving stopped, so the watchdog has nothing left to stop; the lease expires.
                process.lease_validity_deadline.disarm();
                request.report_unconfirmed_release(teardown_result.err().map_or_else(
                    || "the release write was not confirmed".to_owned(),
                    |error| error.to_string(),
                ));
                drop(answered);
                None
            };
            let mut entry = StandbyEntry {
                hold_off_until: Some(Instant::now() + hold_off),
                pending,
            };
            let store_close = wait_for_local_store_close(
                stopped.public_calls_ended,
                &stopped.local_store_close_waiter,
                &process,
                &mut requests,
                &mut entry,
                &mut cancellation,
            )
            .await;
            if let Some(outcome) = end_after_local_store_close(store_close, &mut entry.pending) {
                break (outcome, BoundedTeardown::start());
            }
            match acquire_serving_node(
                &process,
                &grpc_settings,
                &mut requests,
                entry,
                &mut cancellation,
            )
            .await
            {
                Ok(Some(mut serving)) => {
                    serving.activate_public_services(&grpc);
                    node = serving;
                    prune_finished_retirements(&mut retirements).await;
                    let retirement =
                        tokio::spawn(swap_in_serving_instance(&mut grpc, &grpc_settings));
                    retirements.push(retirement);
                }
                Ok(None) => break (Ok(()), BoundedTeardown::start()),
                Err(error) => break (Err(error), BoundedTeardown::start()),
            }
        };
        forward_shutdown.abort();
        drop(grpc);
        for retirement in retirements {
            if let Err(error) = retirement.await {
                tracing::warn!(%error, "retiring a gRPC server failed");
            }
        }
        if let Some(metrics_server) = metrics_server {
            metrics_server.shutdown().await;
        }
        // Every node component and its S3 client is dropped before the emulator stops.
        drop(process);
        if let Some(embedded_s3) = embedded_s3 {
            teardown
                .run(
                    "embedded S3",
                    embedded_s3_stop_budget(&teardown),
                    embedded_s3.shutdown(),
                )
                .await;
        }
        teardown.finish(outcome)
    }
}

/// Waits for the lease and starts serving, following the log meanwhile in hot standby mode.
///
/// Returns `None` when `cancellation` arrives first.
async fn acquire_serving_node(
    process: &ProcessParts,
    grpc_settings: &GrpcSettings,
    requests: &mut mpsc::Receiver<AdministrationRequest>,
    entry: StandbyEntry,
    cancellation: &mut watch::Receiver<bool>,
) -> Result<Option<ServerNode>, ServerError> {
    process.metrics.set_role(NodeRole::Standby);
    match &process.components.hot_standby {
        None => acquire_as_cold_standby(process, requests, entry, cancellation).await,
        Some(standby_config) => {
            acquire_as_hot_standby(
                process,
                grpc_settings,
                standby_config,
                requests,
                entry,
                cancellation,
            )
            .await
        }
    }
}

/// Waits out any hold-off, then opens or restores the store once this process holds the lease.
async fn acquire_as_cold_standby(
    process: &ProcessParts,
    requests: &mut mpsc::Receiver<AdministrationRequest>,
    mut entry: StandbyEntry,
    cancellation: &mut watch::Receiver<bool>,
) -> Result<Option<ServerNode>, ServerError> {
    if !wait_out_hold_off(process, requests, &mut entry, cancellation).await {
        return Ok(None);
    }
    let start = Box::pin(ServerNode::start(
        process,
        EngineStart::Cold,
        &process.components.engine_node,
        cancellation,
    ));
    let started = run_until_started(start, process, requests, &mut entry.pending).await;
    match resolve_start(started, &mut entry.pending)? {
        ServerNodeStart::Serving(node) => {
            process
                .metrics
                .takeovers
                .record(TakeoverStandbyMode::Cold, node.store_source());
            Ok(Some(*node))
        }
        ServerNodeStart::Cancelled(_) => Ok(None),
        ServerNodeStart::FailedBeforeLease { error, .. } => {
            Err(ServerError::EngineStart { source: error })
        }
    }
}

/// Follows the log until this process takes the lease, retrying errors before the lease.
async fn acquire_as_hot_standby(
    process: &ProcessParts,
    grpc_settings: &GrpcSettings,
    standby_config: &HotStandbyConfig,
    requests: &mut mpsc::Receiver<AdministrationRequest>,
    mut entry: StandbyEntry,
    cancellation: &mut watch::Receiver<bool>,
) -> Result<Option<ServerNode>, ServerError> {
    let mut standby = HotStandby::start(
        process.components.engine_node.clone(),
        *standby_config,
        HotStandbyDependencies {
            metrics: process.metrics.clone(),
            object_store: process.object_stores.primary.clone(),
            clock: process.clock.clone(),
            applier: process.applier.clone(),
            id_source: process.id_source.clone(),
            restart_jitter_seed: process.restart_jitter_seed,
        },
    )
    .map_err(|source| ServerError::EngineStart { source })?;
    let publisher = StandbyStatusPublisher::start(
        standby.status_reader(),
        process.metrics.clone(),
        grpc_settings.health.clone(),
    );
    if !wait_out_hold_off(process, requests, &mut entry, cancellation).await {
        publisher.stop().await;
        shut_down_standby(standby).await;
        return Ok(None);
    }
    let mut retry_backoff = TAKEOVER_RETRY_INITIAL_BACKOFF;
    loop {
        let delay = Arc::new(StandbyAcquisitionDelay::new(
            standby.status_reader(),
            process.components.standby.lagging_takeover_delay,
        ));
        let mut engine_config = process.components.engine_node.clone();
        engine_config.lease.acquisition_delay = Some(delay.clone());
        let start = Box::pin(ServerNode::start(
            process,
            EngineStart::HotStandby(standby),
            &engine_config,
            cancellation,
        ));
        let started = run_until_started(start, process, requests, &mut entry.pending).await;
        let started = match resolve_start(started, &mut entry.pending) {
            Ok(started) => started,
            Err(error) => {
                publisher.stop().await;
                return Err(error);
            }
        };
        match started {
            ServerNodeStart::Serving(node) => {
                // The reader keeps reporting a stale standby after the handover, so drop it now.
                publisher.stop().await;
                // A stale or missing standby store makes the engine open or restore its own instead.
                let store_source = node.store_source();
                process
                    .metrics
                    .takeovers
                    .record(TakeoverStandbyMode::Hot, store_source);
                if let Some(available_at) = delay.first_asked_at() {
                    process
                        .metrics
                        .takeover_duration
                        .observe(store_source, available_at.elapsed().as_secs_f64());
                }
                return Ok(Some(*node));
            }
            ServerNodeStart::Cancelled(returned) => {
                publisher.stop().await;
                if let Some(returned) = returned {
                    shut_down_standby(returned).await;
                }
                return Ok(None);
            }
            ServerNodeStart::FailedBeforeLease {
                error,
                standby: returned,
            } => {
                tracing::warn!(
                    %error,
                    retry_in_milliseconds = retry_backoff.as_millis(),
                    "the takeover failed before the node lease; following and retrying"
                );
                standby = returned;
                let has_waited = wait_refusing_requests(
                    retry_backoff,
                    requests,
                    "following as a hot standby",
                    cancellation,
                )
                .await;
                if !has_waited {
                    publisher.stop().await;
                    shut_down_standby(standby).await;
                    return Ok(None);
                }
                retry_backoff = (retry_backoff * 2).min(TAKEOVER_RETRY_MAX_BACKOFF);
            }
        }
    }
}

/// Answers a pending step down from how the start ended, and passes the start on.
fn resolve_start(
    started: Result<ServerNodeStart, ServerError>,
    pending: &mut Option<PendingStepDownAnswer>,
) -> Result<ServerNodeStart, ServerError> {
    let Some(waiting) = pending.take() else {
        return started;
    };
    match &started {
        Ok(ServerNodeStart::Serving(node)) => waiting.answer_taken_back(node.held_lease()),
        Ok(ServerNodeStart::Cancelled(_)) => {
            waiting.answer_unknown("the process stopped before it took the lease back".to_owned());
        }
        Ok(ServerNodeStart::FailedBeforeLease { error, .. }) => {
            waiting.answer_unknown(format!("taking the lease back failed: {error}"));
        }
        Err(error) => waiting.answer_failure(error.to_string()),
    }
    started
}

/// Waits out the step-down hold-off; answers the step down early once a successor appears.
///
/// Returns `false` when `cancellation` arrives first.
async fn wait_out_hold_off(
    process: &ProcessParts,
    requests: &mut mpsc::Receiver<AdministrationRequest>,
    entry: &mut StandbyEntry,
    cancellation: &mut watch::Receiver<bool>,
) -> bool {
    let Some(hold_off_until) = entry.hold_off_until else {
        return true;
    };
    loop {
        if !answer_on_successor(process, &mut entry.pending, Some(&mut *cancellation)).await {
            if let Some(waiting) = entry.pending.take() {
                waiting.answer_unknown("the process stopped during the hold-off".to_owned());
            }
            return false;
        }
        let now = Instant::now();
        if now >= hold_off_until {
            return true;
        }
        let wait = (hold_off_until - now).min(SUCCESSOR_POLL_INTERVAL);
        tokio::select! {
            () = tokio::time::sleep(wait) => {}
            changed = cancellation.changed() => {
                if changed.is_err() || *cancellation.borrow() {
                    if let Some(waiting) = entry.pending.take() {
                        waiting.answer_unknown("the process stopped during the hold-off".to_owned());
                    }
                    return false;
                }
            }
            Some(request) = requests.recv() => {
                refuse_while_standing_by(request, entry.pending.is_some(), "waiting out a step down hold-off");
            }
        }
    }
}

/// Runs `start`, refusing administration calls; answers a pending step down once a successor appears.
async fn run_until_started<F>(
    start: F,
    process: &ProcessParts,
    requests: &mut mpsc::Receiver<AdministrationRequest>,
    pending: &mut Option<PendingStepDownAnswer>,
) -> F::Output
where
    F: std::future::Future,
{
    let mut start = std::pin::pin!(start);
    loop {
        tokio::select! {
            output = &mut start => return output,
            Some(request) = requests.recv() => {
                refuse_while_standing_by(request, pending.is_some(), "waiting for the node lease");
            }
            () = tokio::time::sleep(SUCCESSOR_POLL_INTERVAL), if pending.is_some() => {
                // The start watches cancellation itself; the read timeout bounds this wait.
                answer_on_successor(process, pending, None).await;
            }
        }
    }
}

/// Answers `pending` when the lease names a holder other than this process with a new epoch.
///
/// Returns `false` when `cancellation` arrives first; `pending` then still waits for an answer.
async fn answer_on_successor(
    process: &ProcessParts,
    pending: &mut Option<PendingStepDownAnswer>,
    cancellation: Option<&mut watch::Receiver<bool>>,
) -> bool {
    let Some(waiting) = pending.take() else {
        return true;
    };
    let read = tokio::time::timeout(
        SUCCESSOR_READ_TIMEOUT,
        read_node_lease(
            process.object_stores.primary.as_ref(),
            &process.config.cluster.node_id,
        ),
    );
    tokio::select! {
        read = read => {
            match read {
                Ok(Ok(lease)) => {
                    *pending = waiting.answer_on_takeover(
                        lease.as_ref(),
                        &process.id_source.last_lease_holder_process_id(),
                    );
                }
                Ok(Err(error)) => {
                    tracing::warn!(%error, "could not read the node lease to name a successor");
                    *pending = Some(waiting);
                }
                Err(_) => {
                    tracing::warn!("reading the node lease to name a successor timed out");
                    *pending = Some(waiting);
                }
            }
            true
        }
        () = async {
            match cancellation {
                Some(cancellation) => wait_for_cancellation(cancellation).await,
                None => std::future::pending().await,
            }
        } => {
            *pending = Some(waiting);
            false
        }
    }
}

fn refuse_while_standing_by(request: AdministrationRequest, is_stepping_down: bool, state: &str) {
    match request {
        AdministrationRequest::StepDown(step_down) if is_stepping_down => {
            step_down.refuse_already_stepping_down();
        }
        other => other.refuse_not_active(state),
    }
}

/// Waits `delay`, refusing administration calls; returns `false` when `cancellation` arrives first.
async fn wait_refusing_requests(
    delay: Duration,
    requests: &mut mpsc::Receiver<AdministrationRequest>,
    state: &str,
    cancellation: &mut watch::Receiver<bool>,
) -> bool {
    let mut sleep = std::pin::pin!(tokio::time::sleep(delay));
    loop {
        tokio::select! {
            () = &mut sleep => return true,
            changed = cancellation.changed() => {
                if changed.is_err() || *cancellation.borrow() {
                    return false;
                }
            }
            Some(request) = requests.recv() => request.refuse_not_active(state),
        }
    }
}

/// Replaces the standby instance with a serving one; returns the standby instance's retirement to spawn.
///
/// Activate the period's public services on `grpc` first: until it retires, the standby instance
/// still accepts connections, and the shared gates serve their calls.
fn swap_in_serving_instance(
    grpc: &mut GrpcServer,
    grpc_settings: &GrpcSettings,
) -> impl std::future::Future<Output = ()> + Send + 'static {
    let serving_instance = GrpcServer::start_serving_sharing_gates(grpc_settings, grpc);
    let standby_instance = std::mem::replace(grpc, serving_instance);
    // A standby instance owes no step down answer, so it may send GOAWAY at once.
    let (answered, answer_sent) = oneshot::channel();
    drop(answered);
    standby_instance.retire_after(answer_sent, GRPC_DRAIN_BUDGET)
}

/// Awaits the retirements that already ended, so the list holds only running ones.
async fn prune_finished_retirements(retirements: &mut Vec<JoinHandle<()>>) {
    let (finished, running): (Vec<_>, Vec<_>) =
        retirements.drain(..).partition(JoinHandle::is_finished);
    *retirements = running;
    for retirement in finished {
        if let Err(error) = retirement.await {
            tracing::warn!(%error, "retiring a gRPC server failed");
        }
    }
}

/// Waits until the stepped-down period's public calls end and nothing holds its local store, so
/// reopening it finds no lock.
///
/// Returns [`LocalStoreCloseOutcome::Closed`] once nothing holds the store,
/// [`LocalStoreCloseOutcome::Cancelled`] when `cancellation` arrives first, and
/// [`LocalStoreCloseOutcome::StillOpen`] when a handle still holds it after
/// [`LOCAL_STORE_CLOSE_BUDGET`]; reopening it would then fail, so the caller must not compete for
/// the lease.
async fn wait_for_local_store_close(
    public_calls_ended: impl std::future::Future<Output = ()>,
    waiter: &LocalStoreCloseWaiter,
    process: &ProcessParts,
    requests: &mut mpsc::Receiver<AdministrationRequest>,
    entry: &mut StandbyEntry,
    cancellation: &mut watch::Receiver<bool>,
) -> LocalStoreCloseOutcome {
    let closed = async {
        // Public calls on a stepped-down instance still hold the local store open.
        if tokio::time::timeout(GRPC_DRAIN_BUDGET, public_calls_ended)
            .await
            .is_err()
        {
            tracing::warn!(
                "stepped-down public calls are still open; waiting for the local store to close"
            );
        }
        waiter.wait_until_closed(LOCAL_STORE_CLOSE_BUDGET).await
    };
    let mut closed = std::pin::pin!(closed);
    loop {
        tokio::select! {
            is_closed = &mut closed => {
                return if is_closed {
                    LocalStoreCloseOutcome::Closed
                } else {
                    LocalStoreCloseOutcome::StillOpen
                };
            }
            () = wait_for_cancellation(cancellation) => return LocalStoreCloseOutcome::Cancelled,
            // A silent process would leave describe and a retried step down waiting.
            Some(request) = requests.recv() => {
                refuse_while_standing_by(
                    request,
                    entry.pending.is_some(),
                    "closing the stepped-down local store",
                );
            }
            () = tokio::time::sleep(SUCCESSOR_POLL_INTERVAL), if entry.pending.is_some() => {
                answer_on_successor(process, &mut entry.pending, None).await;
            }
        }
    }
}

/// Answers a pending step down from how the wait for the stepped-down store ended.
///
/// Returns `None` when the store closed, so the process competes for the lease again;
/// `Some(Ok(()))` when cancellation stopped the wait, so the process stops; and
/// `Some(Err(_))` when a handle still holds the store, so the process exits 1 without competing
/// for the lease, which it no longer renews. Only a restart clears such a stuck handle.
fn end_after_local_store_close(
    store_close: LocalStoreCloseOutcome,
    pending: &mut Option<PendingStepDownAnswer>,
) -> Option<Result<(), ServerError>> {
    match store_close {
        LocalStoreCloseOutcome::Closed => None,
        LocalStoreCloseOutcome::Cancelled => {
            if let Some(waiting) = pending.take() {
                waiting.answer_unknown("the process stopped during the step down".to_owned());
            }
            Some(Ok(()))
        }
        LocalStoreCloseOutcome::StillOpen => {
            let error = ServerError::ServingTaskFailed {
                detail: format!(
                    "a task of the stepped-down serving period still held the local store after \
                     {} s; exiting so that a restart releases it",
                    LOCAL_STORE_CLOSE_BUDGET.as_secs()
                ),
            };
            tracing::error!(%error, "the stepped-down local store is still open");
            if let Some(waiting) = pending.take() {
                waiting.answer_failure(error.to_string());
            }
            Some(Err(error))
        }
    }
}

async fn shut_down_standby(standby: HotStandby) {
    if !standby.shutdown().await {
        tracing::warn!("the hot standby's local store did not close in time");
    }
}
