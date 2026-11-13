// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The shared store of each simulated object store semantics.

use std::sync::{Arc, Mutex};
use std::time::Duration;

use dexzd_clock::WallClock;
use dexzd_metrics::NodeMetrics;
use dexzd_object_store::{
    AliyunOssObjectStore, FaultAction, FaultInjector, InMemoryObjectStore,
    InMemoryObjectStoreOptions, ObjectStore, ObjectStoreOperation,
};
use rand::{RngExt, SeedableRng};
use rand_chacha::ChaCha8Rng;

use crate::engine_simulation::SimulatedObjectStoreSemantics;
use crate::engine_simulation::simulated_object_store::NoInjectedFaults;

/// Separates the delay draws of a seed from its other draws.
const PHYSICAL_DELAY_SEED_SALT: u64 = 0x0a11_0055_de1a_0000;
/// Separates the version ID draws of a seed from its other draws.
const VERSION_ID_SEED_SALT: u64 = 0x0a11_0055_1d50_0000;

/// Delays every physical call by a few milliseconds, so versioned writes of processes
/// interleave between their create and its confirmation.
struct PhysicalCallDelays {
    rng: Mutex<ChaCha8Rng>,
}

impl FaultInjector for PhysicalCallDelays {
    fn action(&self, _operation: ObjectStoreOperation, _key: &str) -> FaultAction {
        let delay_millis = self
            .rng
            .lock()
            .expect("physical delay lock is not poisoned")
            .random_range(1..=4);
        FaultAction::Delay(Duration::from_millis(delay_millis))
    }
}

/// Returns the store that every process shares, with `semantics`.
///
/// `clock` stamps the stored writes; `seed` fixes the delays and version IDs of versioned storage.
pub(crate) fn shared_store_with_semantics(
    semantics: SimulatedObjectStoreSemantics,
    seed: u64,
    clock: Arc<dyn WallClock>,
) -> Arc<dyn ObjectStore> {
    match semantics {
        SimulatedObjectStoreSemantics::EtagReplacementOnEveryKey => Arc::new(
            InMemoryObjectStore::with_injector_and_clock(Arc::new(NoInjectedFaults), clock),
        ),
        SimulatedObjectStoreSemantics::CreateOnlyWithVersionedMutableKeys => {
            versioned_shared_store(seed, clock)
        }
    }
}

/// Returns an `AliyunOssObjectStore` over an in-memory store that rejects ETag replacement.
fn versioned_shared_store(seed: u64, clock: Arc<dyn WallClock>) -> Arc<dyn ObjectStore> {
    let physical = InMemoryObjectStore::with_options(InMemoryObjectStoreOptions {
        injector: Some(Arc::new(PhysicalCallDelays {
            rng: Mutex::new(ChaCha8Rng::seed_from_u64(seed ^ PHYSICAL_DELAY_SEED_SALT)),
        })),
        clock: Some(clock),
        rejects_replace_if_etag_matches: true,
    });
    Arc::new(AliyunOssObjectStore::with_seeded_version_ids_for_test(
        Arc::new(physical),
        seed ^ VERSION_ID_SEED_SALT,
        NodeMetrics::unregistered(),
    ))
}
