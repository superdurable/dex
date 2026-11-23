// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::*;

#[test]
fn validates_pull_options() {
    let (cache, _directory) = test_cache();
    let registry = Registry::new()
        .register(RegisteredFlow)
        .expect("register Flow");
    for options in [
        WorkerOptions::new().pull(
            PullWorkerOptions::new()
                .max_concurrent_tasks(2)
                .reserved_rpc_tasks(2),
        ),
        WorkerOptions::new().pull(
            PullWorkerOptions::new()
                .max_concurrent_tasks(2)
                .reserved_rpc_tasks(3),
        ),
        WorkerOptions::new().pull(PullWorkerOptions::new().stop_drain_timeout(Duration::ZERO)),
        WorkerOptions::new().pull(PullWorkerOptions::new().app_worker_id("bad/id")),
        WorkerOptions::new().pull(PullWorkerOptions::new().app_worker_id("RegisteredFlow")),
        WorkerOptions::new().pull(
            PullWorkerOptions::new()
                .max_concurrent_tasks(4)
                .min_open_slots(5),
        ),
        WorkerOptions::new().pull(PullWorkerOptions::new().min_open_slots(0)),
        WorkerOptions::new().pull(
            PullWorkerOptions::new()
                .max_concurrent_tasks(4)
                .min_open_slots(2)
                .reserved_rpc_tasks(2),
        ),
    ] {
        assert!(matches!(
            Worker::try_new(registry.clone(), Arc::clone(&cache), options),
            Err(SdkError::FlowDefinition { .. })
        ));
    }
    let zero_capacity_error = Worker::try_new(
        registry.clone(),
        Arc::clone(&cache),
        WorkerOptions::new().pull(PullWorkerOptions::new().max_concurrent_tasks(0)),
    )
    .err()
    .expect("zero task capacity must fail");
    assert!(
        zero_capacity_error
            .to_string()
            .contains("pull Worker task limit and drain timeout must be positive")
    );
    assert!(
        Worker::try_new(
            registry.clone(),
            Arc::clone(&cache),
            WorkerOptions::new().pull(PullWorkerOptions::new())
        )
        .is_ok()
    );
    // A floor of one reserves no RPC slot by default; a floor of two keeps the reserve.
    for (min_open_slots, rpc_only_slots) in [(1, 0), (2, 1)] {
        let worker = Worker::try_new(
            registry.clone(),
            Arc::clone(&cache),
            WorkerOptions::new().pull(
                PullWorkerOptions::new()
                    .max_concurrent_tasks(4)
                    .min_open_slots(min_open_slots),
            ),
        )
        .expect("adaptive Worker");
        let state = worker.pull_slot_stream_state();
        assert!(state.is_adaptive);
        assert_eq!(rpc_only_slots, state.rpc_only_slots);
        assert_eq!(min_open_slots - rpc_only_slots, state.floor);
        assert_eq!(0, state.open);
    }
}

/// CPU counts that a test machine cannot choose, such as one CPU or more CPUs than the ceiling.
#[test]
fn resolves_default_floor_and_reserved_rpc_tasks() {
    let resolved = |ceiling, floor, reserved, cpus| {
        let slot_floor = slot_streams::resolve_slot_floor(ceiling, floor, reserved, cpus);
        (slot_floor.min_open_slots, slot_floor.reserved_rpc_tasks)
    };
    // Unset floor and R: R = 1 when T >= 2, floor = min(T, max(R + 1, CPUs)).
    assert_eq!((2, 1), resolved(100, None, None, 1));
    assert_eq!((8, 1), resolved(100, None, None, 8));
    assert_eq!((100, 1), resolved(100, None, None, 128));
    assert_eq!((4, 1), resolved(4, None, None, 8));
    assert_eq!((2, 1), resolved(2, None, None, 1));
    assert_eq!((1, 0), resolved(1, None, None, 8));
    // An explicit R raises the floor to R + 1.
    assert_eq!((6, 5), resolved(100, None, Some(5), 2));
    assert_eq!((4, 0), resolved(100, None, Some(0), 4));
    // An explicit floor keeps the phase 1 R rule.
    assert_eq!((1, 0), resolved(100, Some(1), None, 8));
    assert_eq!((100, 1), resolved(100, Some(100), None, 8));
}
