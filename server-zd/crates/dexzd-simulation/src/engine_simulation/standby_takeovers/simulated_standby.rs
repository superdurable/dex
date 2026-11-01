// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! One simulated hot standby process: its disk, its object storage view, and its checks.

use std::collections::BTreeMap;
use std::sync::Arc;

use dexzd_engine::{
    DeterministicCommandApplier, EngineLocalStore, HotStandby, HotStandbyConfig,
    HotStandbyDependencies, HotStandbyState, LocalStoreConfig,
};
use dexzd_storage::InMemoryLocalStore;
use tokio::time::Instant;

use crate::engine_simulation::simulated_clock::SimulatedWallClock;
use crate::engine_simulation::simulated_node::{ClusterEnvironment, engine_node_config};
use crate::engine_simulation::simulated_object_store::NodeObjectStore;
use crate::engine_simulation::standby_takeovers::count_event;
use crate::invariants::InvariantViolation;

/// One standby process, which follows into its own in-memory disk.
pub(super) struct SimulatedStandby {
    pub(super) process_label: String,
    pub(super) standby: HotStandby,
    /// The store its configuration names; a restore follows into a new one, see [`Self::disk`].
    pub(super) configured_store: Arc<EngineLocalStore>,
    pub(super) object_store: Arc<NodeObjectStore>,
    /// The node incarnation its configuration names; a takeover by it serves this incarnation.
    pub(super) incarnation: u32,
    pub(super) outage_ends_at: Option<Instant>,
    /// Stream crossings while following that the checks already counted.
    pub(super) counted_stream_crossings: u64,
}

/// What a standby process starts with.
pub(super) struct StandbyStart {
    pub(super) process_label: String,
    /// A kept disk, or `None` for a new empty one.
    pub(super) disk: Option<Arc<EngineLocalStore>>,
    pub(super) incarnation: u32,
    pub(super) fault_seed: u64,
    pub(super) fault_probability: f64,
    pub(super) restart_jitter_seed: u64,
}

impl SimulatedStandby {
    /// Starts a standby process whose applied frames feed the committed log's shadow.
    pub(super) fn start(environment: &ClusterEnvironment, start: StandbyStart) -> Self {
        let StandbyStart {
            process_label,
            disk,
            incarnation,
            fault_seed,
            fault_probability,
            restart_jitter_seed,
        } = start;
        let configured_store =
            disk.unwrap_or_else(|| Arc::new(EngineLocalStore::Memory(InMemoryLocalStore::new())));
        let object_store =
            environment.node_object_store(&process_label, "standby", fault_seed, fault_probability);
        let standby = HotStandby::start_with_committed_commands_for_test(
            engine_node_config(
                LocalStoreConfig::ExistingForTest(Arc::clone(&configured_store)),
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
                restart_jitter_seed,
            },
            environment.committed_log.standby_sink_for(&process_label),
        )
        .expect("the simulated node configuration is valid");
        environment.trace.record(&format!(
            "{process_label} starts as the hot standby of incarnation {incarnation}"
        ));
        Self {
            process_label,
            standby,
            configured_store,
            object_store,
            incarnation,
            outage_ends_at: None,
            counted_stream_crossings: 0,
        }
    }

    /// Returns the store the standby follows into: its configured one, or the one a restore made.
    pub(super) fn disk(&self) -> Arc<EngineLocalStore> {
        self.standby
            .local_store_for_test()
            .unwrap_or_else(|| Arc::clone(&self.configured_store))
    }

    /// Checks that the standby never wrote object storage and never failed for good.
    pub(super) fn check(
        &mut self,
        events: &mut BTreeMap<String, u64>,
    ) -> Result<(), InvariantViolation> {
        check_wrote_nothing(&self.process_label, &self.object_store)?;
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
        count_event(events, &format!("check-{:?}", status.state).to_lowercase());
        let crossings = self.standby.probe_for_test().stream_crossings_for_test();
        for _ in self.counted_stream_crossings..crossings {
            count_event(events, "standby-crossed-stream-while-following");
        }
        self.counted_stream_crossings = crossings;
        Ok(())
    }

    pub(super) fn begin_outage(&mut self, environment: &ClusterEnvironment, ends_at: Instant) {
        self.object_store.set_outage(true);
        self.outage_ends_at = Some(ends_at);
        environment
            .trace
            .record(&format!("{} loses object storage", self.process_label));
    }

    pub(super) fn end_outage(&mut self, environment: &ClusterEnvironment) {
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
    pub(super) fn crash(self, environment: &ClusterEnvironment) -> Arc<EngineLocalStore> {
        environment
            .trace
            .record(&format!("{} crashes", self.process_label));
        let disk = self.disk();
        self.object_store.cut_off();
        drop(self.standby);
        disk
    }
}

/// Checks that the standby process `process_label` sent no put or delete to object storage.
pub(super) fn check_wrote_nothing(
    process_label: &str,
    object_store: &NodeObjectStore,
) -> Result<(), InvariantViolation> {
    match object_store.write_calls() {
        0 => Ok(()),
        writes => Err(InvariantViolation::new(
            "standby-wrote-object-storage",
            format!("{process_label} sent {writes} object storage writes"),
        )),
    }
}
