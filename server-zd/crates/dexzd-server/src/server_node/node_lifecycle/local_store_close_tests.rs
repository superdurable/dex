// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The wait for a stepped-down period's local store, which no process test can hold past its bound.

use std::path::Path;
use std::sync::Arc;
use std::time::Duration;

use dexzd_api::{
    ActiveNodeAdministration, ActiveNodeStepDownError, ActiveNodeStepDownOutcome,
    LeaseHolderAfterStepDown, StepDownPrecondition,
};
use dexzd_clock::SystemWallClock;
use dexzd_cluster_metadata::LeaseValidityDeadline;
use dexzd_engine::{DeterministicCommandApplier, LocalStoreCloseWaiter};
use dexzd_metrics::NodeMetrics;
use dexzd_object_store::{InMemoryObjectStore, ObjectStore};
use dexzd_storage::{RocksDbLocalStore, RocksDbStoreOptions};
use tokio::sync::{oneshot, watch};
use tokio::task::JoinHandle;
use tokio::time::Instant;

use super::{
    AdministrationRequest, HeldNodeLease, LeaseHolderRecordingIdSource,
    LifecycleActiveNodeAdministration, LocalStoreCloseOutcome, PendingStepDownAnswer, ProcessParts,
    StandbyEntry, end_after_local_store_close, wait_for_local_store_close,
};
use crate::ServerError;
use crate::component_configs::ComponentConfigs;
use crate::config::Config;
use crate::object_stores::ObjectStores;

const CONFIG_YAML: &str = "cluster:\n  nodeId: node-a\nobjectStore:\n  bucket: dex-test\n";
/// The documented 4 s for public calls plus 60 s for the store; a stuck task's I/O ends first.
const DOCUMENTED_STILL_OPEN_WAIT: Duration = Duration::from_secs(64);
/// Well inside the store close budget, so only cancellation can end the wait then.
const CANCELLED_AFTER: Duration = Duration::from_secs(10);
const STEP_DOWN_HOLD_OFF: Duration = Duration::from_secs(5);

type StepDownReply = Result<ActiveNodeStepDownOutcome, ActiveNodeStepDownError>;

/// A store that stays open ends the wait as still open after the public call and store budgets.
#[tokio::test(start_paused = true)]
async fn a_store_that_stays_open_ends_the_wait_after_its_budgets() {
    let directory = tempfile::tempdir().expect("a temporary test directory can be created");
    let (waiter, _open_store) = waiter_of_an_open_store(directory.path());
    let process = process_parts(directory.path());
    let (_administration, mut requests) = LifecycleActiveNodeAdministration::new();
    let (_cancel, mut cancellation) = watch::channel(false);
    let started = Instant::now();
    let outcome = wait_for_local_store_close(
        std::future::pending::<()>(),
        &waiter,
        &process,
        &mut requests,
        &mut StandbyEntry::default(),
        &mut cancellation,
    )
    .await;
    let waited = started.elapsed();
    assert!(
        matches!(outcome, LocalStoreCloseOutcome::StillOpen),
        "a store that stays open is reported still open"
    );
    assert!(
        (DOCUMENTED_STILL_OPEN_WAIT..DOCUMENTED_STILL_OPEN_WAIT + Duration::from_secs(1))
            .contains(&waited),
        "the wait must last {DOCUMENTED_STILL_OPEN_WAIT:?}, but lasted {waited:?}"
    );
}

/// Cancellation ends the wait for a store that stays open.
#[tokio::test(start_paused = true)]
async fn cancellation_ends_the_wait_for_a_store_that_stays_open() {
    let directory = tempfile::tempdir().expect("a temporary test directory can be created");
    let (waiter, _open_store) = waiter_of_an_open_store(directory.path());
    let process = process_parts(directory.path());
    let (_administration, mut requests) = LifecycleActiveNodeAdministration::new();
    let (cancel, mut cancellation) = watch::channel(false);
    let mut entry = StandbyEntry::default();
    let started = Instant::now();
    let cancel_later = async {
        tokio::time::sleep(CANCELLED_AFTER).await;
        cancel.send_replace(true);
    };
    let (outcome, ()) = tokio::join!(
        wait_for_local_store_close(
            std::future::pending::<()>(),
            &waiter,
            &process,
            &mut requests,
            &mut entry,
            &mut cancellation,
        ),
        cancel_later,
    );
    assert!(
        matches!(outcome, LocalStoreCloseOutcome::Cancelled),
        "cancellation ends the wait"
    );
    assert_eq!(started.elapsed(), CANCELLED_AFTER);
}

/// A store still open answers the step down `Internal` and ends the process with an error.
#[tokio::test]
async fn a_store_still_open_answers_internal_and_exits_with_an_error() {
    let (mut pending, step_down) = pending_step_down().await;
    let ended = end_after_local_store_close(LocalStoreCloseOutcome::StillOpen, &mut pending);
    assert!(
        matches!(ended, Some(Err(ServerError::ServingTaskFailed { .. }))),
        "the process exits with an error, which exits 1: {ended:?}"
    );
    assert!(pending.is_none(), "the step down is answered");
    let reply = step_down.await.expect("the step down call does not panic");
    assert!(
        matches!(reply, Err(ActiveNodeStepDownError::Internal { .. })),
        "{reply:?}"
    );
}

/// A closed store leaves the step down waiting for the lease contest.
#[tokio::test]
async fn a_closed_store_lets_the_process_compete_for_the_lease() {
    let (mut pending, step_down) = pending_step_down().await;
    let ended = end_after_local_store_close(LocalStoreCloseOutcome::Closed, &mut pending);
    assert!(ended.is_none(), "the process competes for the lease again");
    assert!(
        pending.is_some(),
        "the take-back or a successor answers later"
    );
    step_down.abort();
}

/// Cancellation answers the step down as unknown and stops the process cleanly.
#[tokio::test]
async fn a_cancelled_wait_answers_unknown_and_stops_the_process() {
    let (mut pending, step_down) = pending_step_down().await;
    let ended = end_after_local_store_close(LocalStoreCloseOutcome::Cancelled, &mut pending);
    assert!(matches!(ended, Some(Ok(()))), "{ended:?}");
    let reply = step_down.await.expect("the step down call does not panic");
    assert!(
        matches!(
            reply,
            Ok(ActiveNodeStepDownOutcome {
                lease_holder_after_step_down: LeaseHolderAfterStepDown::Unknown { .. },
                ..
            })
        ),
        "{reply:?}"
    );
}

/// Opens a RocksDB store under `directory`; the waiter sees it open while the store lives.
fn waiter_of_an_open_store(directory: &Path) -> (LocalStoreCloseWaiter, RocksDbLocalStore) {
    let store_directory = directory.join("store");
    let open_store = RocksDbLocalStore::open(&store_directory, RocksDbStoreOptions::default())
        .expect("the test store opens");
    (
        LocalStoreCloseWaiter::for_directory_for_test(store_directory),
        open_store,
    )
}

fn process_parts(directory: &Path) -> ProcessParts {
    let config = Config::parse_yaml(CONFIG_YAML).expect("the test configuration parses");
    let components =
        ComponentConfigs::from_config(&config, false).expect("the test configuration resolves");
    let (_registry, metrics) = NodeMetrics::registered(directory.to_path_buf(), 1);
    let object_store: Arc<dyn ObjectStore> = Arc::new(InMemoryObjectStore::new());
    ProcessParts {
        config,
        components,
        metrics: Arc::new(metrics),
        lease_validity_deadline: LeaseValidityDeadline::disarmed(),
        object_stores: ObjectStores {
            primary: object_store.clone(),
            hard_delete: object_store,
        },
        clock: Arc::new(SystemWallClock),
        applier: Arc::new(DeterministicCommandApplier),
        id_source: Arc::new(LeaseHolderRecordingIdSource::default()),
        restart_jitter_seed: 0,
    }
}

/// Returns a step down whose answer waits, and the AdminService call that receives it.
async fn pending_step_down() -> (Option<PendingStepDownAnswer>, JoinHandle<StepDownReply>) {
    let (administration, mut requests) = LifecycleActiveNodeAdministration::new();
    let step_down = tokio::spawn(async move {
        administration
            .step_down_active_node(StepDownPrecondition::default())
            .await
    });
    let Some(AdministrationRequest::StepDown(request)) = requests.recv().await else {
        panic!("the lifecycle receives the step down");
    };
    let released = HeldNodeLease {
        node_id: "node-a".to_owned(),
        node_epoch: 1,
        holder_process_id: "holder".to_owned(),
    };
    let (answered, _answer_sent) = oneshot::channel();
    let pending = PendingStepDownAnswer::new(request, &released, STEP_DOWN_HOLD_OFF, answered);
    (Some(pending), step_down)
}
