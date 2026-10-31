// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{BTreeMap, BTreeSet};

use dexzd_keyspace::{FlowHash, PartitionId, RunId, TenantId};
use dexzd_metrics::ProjectionDropReason;

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub(crate) enum RepairPriority {
    Start,
    Close,
    Intermediate,
}

impl RepairPriority {
    pub(crate) fn reason(self) -> ProjectionDropReason {
        match self {
            Self::Start => ProjectionDropReason::Start,
            Self::Close => ProjectionDropReason::Close,
            Self::Intermediate => ProjectionDropReason::Intermediate,
        }
    }
}

#[derive(Clone, Copy)]
pub(crate) struct ProjectionRepair {
    pub(crate) tenant_id: TenantId,
    pub(crate) run_id: RunId,
    pub(crate) flow_hash: FlowHash,
    pub(crate) partition: PartitionId,
    pub(crate) priority: RepairPriority,
    generation: u64,
}

struct PendingRepair {
    repair: ProjectionRepair,
    dropped_sequence: u64,
    is_in_flight: bool,
}

#[derive(Default)]
pub(crate) struct ProjectionRepairs {
    entries: BTreeMap<(TenantId, RunId), PendingRepair>,
    order: BTreeSet<(RepairPriority, TenantId, RunId)>,
    last_generation: u64,
}

impl ProjectionRepairs {
    pub(crate) fn record(
        &mut self,
        mut repair: ProjectionRepair,
        sequence: u64,
        limit: usize,
    ) -> bool {
        let identity = (repair.tenant_id, repair.run_id);
        if !self.entries.contains_key(&identity) && self.entries.len() >= limit {
            return false;
        }
        self.last_generation += 1;
        repair.generation = self.last_generation;
        match self.entries.get_mut(&identity) {
            Some(pending) => {
                self.order
                    .remove(&(pending.repair.priority, identity.0, identity.1));
                pending.repair.priority = pending.repair.priority.min(repair.priority);
                pending.repair.generation = repair.generation;
                pending.dropped_sequence = sequence;
                if !pending.is_in_flight {
                    self.order
                        .insert((pending.repair.priority, identity.0, identity.1));
                }
            }
            None => {
                self.order.insert((repair.priority, identity.0, identity.1));
                self.entries.insert(
                    identity,
                    PendingRepair {
                        repair,
                        dropped_sequence: sequence,
                        is_in_flight: false,
                    },
                );
            }
        }
        true
    }

    pub(crate) fn take_batch(&mut self, limit: usize) -> Vec<ProjectionRepair> {
        let mut repairs = Vec::new();
        while repairs.len() < limit {
            let Some((_, tenant_id, run_id)) = self.order.pop_first() else {
                break;
            };
            let pending = self
                .entries
                .get_mut(&(tenant_id, run_id))
                .expect("ordered repair exists");
            pending.is_in_flight = true;
            repairs.push(pending.repair);
        }
        repairs
    }

    pub(crate) fn finish_batch(&mut self, repairs: &[ProjectionRepair]) {
        for repair in repairs {
            let identity = (repair.tenant_id, repair.run_id);
            let Some(pending) = self.entries.get_mut(&identity) else {
                continue;
            };
            if pending.repair.generation == repair.generation {
                self.entries.remove(&identity);
            } else {
                pending.is_in_flight = false;
                self.order
                    .insert((pending.repair.priority, identity.0, identity.1));
            }
        }
    }

    pub(crate) fn retry_batch(&mut self, repairs: &[ProjectionRepair]) {
        for repair in repairs {
            let identity = (repair.tenant_id, repair.run_id);
            if let Some(pending) = self.entries.get_mut(&identity) {
                pending.is_in_flight = false;
                self.order
                    .insert((pending.repair.priority, identity.0, identity.1));
            }
        }
    }

    pub(crate) fn finish_through(&mut self, partition: Option<PartitionId>, sequence: u64) {
        self.entries.retain(|identity, pending| {
            let is_covered = pending.dropped_sequence <= sequence
                && partition.is_none_or(|partition| pending.repair.partition == partition);
            if is_covered {
                self.order
                    .remove(&(pending.repair.priority, identity.0, identity.1));
            }
            !is_covered
        });
    }

    pub(crate) fn len(&self) -> usize {
        self.entries.len()
    }
}

impl ProjectionRepair {
    pub(crate) fn new(
        tenant_id: TenantId,
        run_id: RunId,
        flow_hash: FlowHash,
        partition: PartitionId,
        priority: RepairPriority,
    ) -> Self {
        Self {
            tenant_id,
            run_id,
            flow_hash,
            partition,
            priority,
            generation: 0,
        }
    }
}
