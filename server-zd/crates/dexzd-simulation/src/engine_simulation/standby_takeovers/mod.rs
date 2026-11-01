// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Hot standbys of the simulated node: they follow its log, crash, lose object storage, and
//! take over when the serving process crashes.

use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::Duration;

use dexzd_engine::{
    DeterministicCommandApplier, EngineLocalStore, HotStandby, HotStandbyConfig,
    HotStandbyDependencies, HotStandbyState, LocalStoreConfig,
};
use dexzd_storage::InMemoryLocalStore;
use rand::{RngExt, SeedableRng};
use rand_chacha::ChaCha8Rng;
use tokio::time::Instant;

use crate::engine_simulation::simulated_clock::SimulatedWallClock;
use crate::engine_simulation::simulated_node::{
    ClusterEnvironment, FailedTakeover, LocalStoreStart, NodeProcess, ProcessStart,
    copy_local_store, engine_node_config, start_error_kind, wait_before_next_start,
};
use crate::engine_simulation::simulated_object_store::NodeObjectStore;
use crate::invariants::InvariantViolation;

/// Takeover attempts that fail before the lease before the node counts as unable to recover.
const MAX_TAKEOVER_ATTEMPTS: u32 = 40;
/// Probability that a takeover crashes at a random point.
const TAKEOVER_CRASH_PROBABILITY: f64 = 0.3;
/// Probability that the next crash of the serving process ends in a takeover.
const TAKEOVER_PROBABILITY: f64 = 0.7;
/// Longest wait after the calm tail for the standby to follow the log again.
const STANDBY_RECOVERY_DEADLINE: Duration = Duration::from_secs(300);

/// How a takeover from the hot standby ended.
pub(crate) enum StandbyTakeover {
    /// The process serves.
    Serving(NodeProcess),
    /// The takeover crashed or failed after the lease; a process restarts on this disk.
    Restart(Arc<EngineLocalStore>),
}

/// The node's hot standby and the seeded schedule of what happens to it.
pub(crate) struct StandbyTakeovers {
    rng: ChaCha8Rng,
    standby: Option<SimulatedStandby>,
    started_standbys: u64,
    started_takeovers: u64,
    next_event: Instant,
    /// Standby crashes and outages stop here, so the calm tail can check recovery.
    active_end: Instant,
}

/// One standby process, which follows into its own in-memory disk.
struct SimulatedStandby {
    process_label: String,
    standby: HotStandby,
    store: Arc<EngineLocalStore>,
    object_store: Arc<NodeObjectStore>,
    outage_ends_at: Option<Instant>,
}

impl StandbyTakeovers {
    pub(crate) fn new(seed: u64, active_end: Instant) -> Self {
        let mut rng = ChaCha8Rng::seed_from_u64(seed);
        let next_event = Instant::now() + Duration::from_millis(rng.random_range(5_000..20_000));
        Self {
            rng,
            standby: None,
            started_standbys: 0,
            started_takeovers: 0,
            next_event,
            active_end,
        }
    }

    /// Starts a standby process on `store`, a kept disk, or on a new empty disk with `None`.
    pub(crate) fn start_standby(
        &mut self,
        environment: &ClusterEnvironment,
        incarnation: u32,
        fault_probability: f64,
        store: Option<Arc<EngineLocalStore>>,
        events: &mut BTreeMap<String, u64>,
    ) {
        self.started_standbys += 1;
        let process_label = format!("s{}", self.started_standbys);
        let store =
            store.unwrap_or_else(|| Arc::new(EngineLocalStore::Memory(InMemoryLocalStore::new())));
        let object_store = environment.node_object_store(
            &process_label,
            "standby",
            self.rng.random(),
            fault_probability,
        );
        let standby = HotStandby::start(
            engine_node_config(
                LocalStoreConfig::ExistingForTest(Arc::clone(&store)),
                incarnation,
                environment.profile,
            ),
            HotStandbyConfig::default(),
            HotStandbyDependencies {
                metrics: Arc::new(dexzd_metrics::NodeMetrics::unregistered()),
                object_store: object_store.clone(),
                clock: Arc::new(SimulatedWallClock::new(environment.origin, 0)),
                applier: Arc::new(DeterministicCommandApplier),
                id_source: Arc::clone(&environment.id_source),
                restart_jitter_seed: self.rng.random(),
            },
        )
        .expect("the simulated node configuration is valid");
        environment
            .trace
            .record(&format!("{process_label} starts as the hot standby"));
        count(events, "standby-start");
        self.standby = Some(SimulatedStandby {
            process_label,
            standby,
            store,
            object_store,
            outage_ends_at: None,
        });
    }

    /// Draws whether the next crash of the serving process ends in a takeover.
    pub(crate) fn should_take_over(&mut self) -> bool {
        self.standby.is_some() && self.rng.random_bool(TAKEOVER_PROBABILITY)
    }

    /// Checks that the standby never wrote object storage and never failed, then runs its due
    /// crash or outage.
    pub(crate) fn check_and_step(
        &mut self,
        environment: &ClusterEnvironment,
        incarnation: u32,
        fault_probability: f64,
        events: &mut BTreeMap<String, u64>,
    ) -> Result<(), InvariantViolation> {
        let now = Instant::now();
        let Some(standby) = &mut self.standby else {
            return Ok(());
        };
        standby.check(events)?;
        if standby
            .outage_ends_at
            .is_some_and(|ends_at| now >= ends_at || now >= self.active_end)
        {
            standby.end_outage(environment);
        }
        if now < self.next_event || now >= self.active_end {
            return Ok(());
        }
        self.next_event = now + Duration::from_millis(self.rng.random_range(10_000..40_000));
        match self.rng.random_range(0..10) {
            0..=2 => {
                let crashed = self
                    .standby
                    .take()
                    .expect("checked above")
                    .crash(environment);
                let store = self
                    .rng
                    .random_bool(0.5)
                    .then(|| copy_local_store(&crashed));
                count(
                    events,
                    if store.is_some() {
                        "standby-crash-kept"
                    } else {
                        "standby-crash-lost"
                    },
                );
                self.start_standby(environment, incarnation, fault_probability, store, events);
            }
            3..=5 if standby.outage_ends_at.is_none() => {
                let lasts = Duration::from_millis(self.rng.random_range(20_000..150_000));
                standby.begin_outage(environment, now + lasts);
                count(events, "standby-outage");
            }
            _ => {}
        }
        Ok(())
    }

    /// Crashes the takeover at a random point now and then; retries one that failed before the
    /// lease with the standby it returned.
    pub(crate) async fn take_over(
        &mut self,
        environment: &ClusterEnvironment,
        incarnation: u32,
        clock_offset_micros: i64,
        fault_probability: f64,
        events: &mut BTreeMap<String, u64>,
    ) -> Result<StandbyTakeover, InvariantViolation> {
        let mut standby = self
            .standby
            .take()
            .expect("a takeover is drawn only while a standby runs");
        standby.end_outage(environment);
        let clock = SimulatedWallClock::new(environment.origin, clock_offset_micros);
        let mut last_error = String::new();
        for _ in 0..MAX_TAKEOVER_ATTEMPTS {
            self.started_takeovers += 1;
            let process_label = format!("t{}", self.started_takeovers);
            let object_store = environment.node_object_store(
                &process_label,
                "takeover",
                self.rng.random(),
                fault_probability,
            );
            let start = ProcessStart {
                process_label: process_label.clone(),
                local_store: LocalStoreStart::Existing(Arc::clone(&standby.store)),
                incarnation,
                clock_offset_micros,
                object_store: Arc::clone(&object_store),
            };
            let crash_after = self
                .rng
                .random_bool(TAKEOVER_CRASH_PROBABILITY)
                .then(|| Duration::from_millis(self.rng.random_range(0..300)));
            let SimulatedStandby {
                process_label: standby_label,
                standby: hot_standby,
                store,
                object_store: standby_object_store,
                ..
            } = standby;
            let taking_over = NodeProcess::take_over(environment, start, hot_standby);
            let outcome = match crash_after {
                Some(delay) => tokio::select! {
                    biased;
                    outcome = taking_over => Some(outcome),
                    () = tokio::time::sleep(delay) => None,
                },
                None => Some(taking_over.await),
            };
            let error = match outcome {
                None => {
                    // The process died mid-takeover; its disk keeps what the standby wrote.
                    object_store.cut_off();
                    standby_object_store.cut_off();
                    environment
                        .trace
                        .record(&format!("{process_label} crashes while it takes over"));
                    count(events, "takeover-crash");
                    return Ok(StandbyTakeover::Restart(store));
                }
                Some(Ok(process)) => {
                    standby_object_store.cut_off();
                    process.object_store.mark_serving();
                    process
                        .object_store
                        .set_fault_probability(fault_probability);
                    let kind = if process.is_serving_hot_standby_store() {
                        "warm-takeover"
                    } else {
                        "cold-takeover"
                    };
                    environment
                        .trace
                        .record(&format!("{process_label} serves after a {kind}"));
                    count(events, kind);
                    return Ok(StandbyTakeover::Serving(process));
                }
                Some(Err(FailedTakeover {
                    error,
                    standby: None,
                })) => {
                    standby_object_store.cut_off();
                    environment.trace.record(&format!(
                        "{process_label} failed to take over after the lease: {error}"
                    ));
                    count(events, "takeover-failed-after-lease");
                    return Ok(StandbyTakeover::Restart(store));
                }
                Some(Err(FailedTakeover {
                    error,
                    standby: Some(returned),
                })) => {
                    standby = SimulatedStandby {
                        process_label: standby_label,
                        standby: returned,
                        store,
                        object_store: standby_object_store,
                        outage_ends_at: None,
                    };
                    error
                }
            };
            let kind = start_error_kind(&error);
            environment.trace.record(&format!(
                "{process_label} failed to take over before the lease: {kind}: {error}"
            ));
            count(events, &format!("takeover-failed-before-lease-{kind}"));
            last_error = error.to_string();
            tokio::time::sleep(wait_before_next_start(&error, &clock, &mut self.rng)).await;
        }
        Err(InvariantViolation::new(
            "standby-cannot-take-over",
            format!("{MAX_TAKEOVER_ATTEMPTS} takeovers failed; the last failed with: {last_error}"),
        ))
    }

    /// Waits until the standby follows the log again after the calm tail.
    pub(crate) async fn wait_until_following(
        &mut self,
        environment: &ClusterEnvironment,
        events: &mut BTreeMap<String, u64>,
    ) -> Result<(), InvariantViolation> {
        let Some(standby) = &mut self.standby else {
            return Ok(());
        };
        standby.end_outage(environment);
        let deadline = Instant::now() + STANDBY_RECOVERY_DEADLINE;
        loop {
            standby.check(events)?;
            let status = standby.standby.status();
            if status.state == HotStandbyState::Following {
                count(events, "standby-following-at-end");
                return Ok(());
            }
            if Instant::now() >= deadline {
                return Err(InvariantViolation::new(
                    "standby-did-not-recover",
                    format!(
                        "{} did not follow the log again: {status:?}",
                        standby.process_label
                    ),
                ));
            }
            tokio::time::sleep(Duration::from_secs(1)).await;
        }
    }
}

impl SimulatedStandby {
    /// Checks that the standby never wrote object storage and never failed for good.
    fn check(&self, events: &mut BTreeMap<String, u64>) -> Result<(), InvariantViolation> {
        let rejected_writes = self.standby.rejected_writes_for_test();
        if rejected_writes > 0 {
            return Err(InvariantViolation::new(
                "standby-wrote-object-storage",
                format!(
                    "{} attempted {rejected_writes} object storage writes",
                    self.process_label
                ),
            ));
        }
        let status = self.standby.status();
        if status.state == HotStandbyState::Failed {
            return Err(InvariantViolation::new(
                "standby-failed",
                format!("{} failed: {status:?}", self.process_label),
            ));
        }
        count(events, &format!("check-{:?}", status.state).to_lowercase());
        Ok(())
    }

    fn begin_outage(&mut self, environment: &ClusterEnvironment, ends_at: Instant) {
        self.object_store.set_outage(true);
        self.outage_ends_at = Some(ends_at);
        environment
            .trace
            .record(&format!("{} loses object storage", self.process_label));
    }

    fn end_outage(&mut self, environment: &ClusterEnvironment) {
        if self.outage_ends_at.take().is_some() {
            self.object_store.set_outage(false);
            environment.trace.record(&format!(
                "{} reaches object storage again",
                self.process_label
            ));
        }
    }

    /// Kills the standby: its writes stop at once and its storage calls hang.
    ///
    /// Returns its disk.
    fn crash(self, environment: &ClusterEnvironment) -> Arc<EngineLocalStore> {
        environment
            .trace
            .record(&format!("{} crashes", self.process_label));
        self.object_store.cut_off();
        drop(self.standby);
        self.store
    }
}

fn count(events: &mut BTreeMap<String, u64>, event: &str) {
    *events.entry(event.to_owned()).or_default() += 1;
}
