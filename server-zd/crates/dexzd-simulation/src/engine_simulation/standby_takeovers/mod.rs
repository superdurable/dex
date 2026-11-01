// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Hot standbys of the simulated node, which crash, lose storage, and take over from it.

mod simulated_standby;

use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::Duration;

use dexzd_engine::{EngineLocalStore, HotStandbyState};
use rand::{RngExt, SeedableRng};
use rand_chacha::ChaCha8Rng;
use tokio::time::Instant;

use crate::engine_simulation::simulated_clock::SimulatedWallClock;
use crate::engine_simulation::simulated_node::{
    ClusterEnvironment, FailedTakeover, LocalStoreStart, NodeProcess, ProcessStart,
    copy_local_store, start_error_kind, wait_before_next_start,
};
use crate::engine_simulation::simulated_object_store::has_discarded_existing_store;
use crate::invariants::InvariantViolation;
use simulated_standby::{SimulatedStandby, StandbyStart, check_wrote_nothing};

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
    Serving(Box<NodeProcess>),
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

    /// Starts a standby of the serving incarnation on a kept `disk`, or on an empty one with `None`.
    ///
    /// A standby and its takeover share one configuration, as one real process does.
    pub(crate) fn start_standby(
        &mut self,
        environment: &ClusterEnvironment,
        serving_incarnation: u32,
        fault_probability: f64,
        disk: Option<Arc<EngineLocalStore>>,
        events: &mut BTreeMap<String, u64>,
    ) {
        self.started_standbys += 1;
        let start = StandbyStart {
            process_label: format!("s{}", self.started_standbys),
            disk,
            incarnation: serving_incarnation,
            fault_seed: self.rng.random(),
            fault_probability,
            restart_jitter_seed: self.rng.random(),
        };
        self.standby = Some(SimulatedStandby::start(environment, start));
        count_event(events, "standby-start");
    }

    /// Redeploys the standby on its disk with a new serving incarnation, before a start opens its
    /// stream, so the standby then follows across the segment boundary.
    pub(crate) fn follow_incarnation(
        &mut self,
        environment: &ClusterEnvironment,
        serving_incarnation: u32,
        fault_probability: f64,
        events: &mut BTreeMap<String, u64>,
    ) {
        let Some(standby) = self
            .standby
            .take_if(|standby| standby.incarnation < serving_incarnation)
        else {
            return;
        };
        let disk = copy_local_store(&standby.crash(environment));
        count_event(events, "standby-redeploy");
        self.start_standby(
            environment,
            serving_incarnation,
            fault_probability,
            Some(disk),
            events,
        );
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
        serving_incarnation: u32,
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
                let disk = self
                    .rng
                    .random_bool(0.5)
                    .then(|| copy_local_store(&crashed));
                let event = if disk.is_some() {
                    "standby-crash-kept"
                } else {
                    "standby-crash-lost"
                };
                count_event(events, event);
                self.start_standby(
                    environment,
                    serving_incarnation,
                    fault_probability,
                    disk,
                    events,
                );
            }
            3..=5 if standby.outage_ends_at.is_none() => {
                let lasts = Duration::from_millis(self.rng.random_range(20_000..150_000));
                standby.begin_outage(environment, now + lasts);
                count_event(events, "standby-outage");
            }
            _ => {}
        }
        Ok(())
    }

    /// Takes over from the standby, then starts a new standby on an empty disk.
    pub(crate) async fn take_over(
        &mut self,
        environment: &ClusterEnvironment,
        incarnation: u32,
        clock_offset_micros: i64,
        fault_probability: f64,
        events: &mut BTreeMap<String, u64>,
    ) -> Result<StandbyTakeover, InvariantViolation> {
        let takeover = self
            .take_over_from_standby(
                environment,
                incarnation,
                clock_offset_micros,
                fault_probability,
                events,
            )
            .await?;
        self.start_standby(environment, incarnation, fault_probability, None, events);
        Ok(takeover)
    }

    /// Takes over from the standby, crashing at random points; retries failures before the lease
    /// with the returned standby.
    async fn take_over_from_standby(
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
                local_store: LocalStoreStart::Existing(Arc::clone(&standby.configured_store)),
                incarnation,
                clock_offset_micros,
                object_store: Arc::clone(&object_store),
            };
            let crash_after = self
                .rng
                .random_bool(TAKEOVER_CRASH_PROBABILITY)
                .then(|| Duration::from_millis(self.rng.random_range(0..300)));
            check_wrote_nothing(&standby.process_label, &standby.object_store)?;
            let disk = standby.disk();
            let SimulatedStandby {
                process_label: standby_label,
                standby: hot_standby,
                configured_store,
                object_store: standby_object_store,
                incarnation: standby_incarnation,
                counted_stream_crossings,
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
            // The handover stops the standby, which must not write while it stops either.
            check_wrote_nothing(&standby_label, &standby_object_store)?;
            let error = match outcome {
                None => {
                    // The process died mid-takeover; its disk keeps what the standby wrote.
                    object_store.cut_off();
                    standby_object_store.cut_off();
                    environment
                        .trace
                        .record(&format!("{process_label} crashes while it takes over"));
                    count_event(events, "takeover-crash");
                    return Ok(StandbyTakeover::Restart(disk));
                }
                Some(Ok(process)) => {
                    standby_object_store.cut_off();
                    let manifest = object_store.last_read_manifest();
                    if has_discarded_existing_store(
                        manifest.as_ref(),
                        Some(&disk),
                        &process.local_store,
                    )? {
                        count_event(events, "takeover-discarded-store");
                    }
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
                    count_event(events, kind);
                    return Ok(StandbyTakeover::Serving(Box::new(process)));
                }
                Some(Err(FailedTakeover {
                    error,
                    standby: None,
                })) => {
                    standby_object_store.cut_off();
                    environment.trace.record(&format!(
                        "{process_label} failed to take over after the lease: {error}"
                    ));
                    count_event(events, "takeover-failed-after-lease");
                    return Ok(StandbyTakeover::Restart(disk));
                }
                Some(Err(FailedTakeover {
                    error,
                    standby: Some(returned),
                })) => {
                    standby = SimulatedStandby {
                        process_label: standby_label,
                        standby: returned,
                        configured_store,
                        object_store: standby_object_store,
                        incarnation: standby_incarnation,
                        outage_ends_at: None,
                        counted_stream_crossings,
                    };
                    error
                }
            };
            let kind = start_error_kind(&error);
            environment.trace.record(&format!(
                "{process_label} failed to take over before the lease: {kind}: {error}"
            ));
            count_event(events, &format!("takeover-failed-before-lease-{kind}"));
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
                count_event(events, "standby-following-at-end");
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

fn count_event(events: &mut BTreeMap<String, u64>, event: &str) {
    *events.entry(event.to_owned()).or_default() += 1;
}
