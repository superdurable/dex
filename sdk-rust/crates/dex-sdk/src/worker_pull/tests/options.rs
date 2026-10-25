// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::*;

#[test]
fn validates_pull_options_and_target_conflicts() {
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
        WorkerOptions::new().pull(PullWorkerOptions::new().idle_streams_per_queue(0)),
        WorkerOptions::new().pull(PullWorkerOptions::new().stop_drain_timeout(Duration::ZERO)),
        WorkerOptions::new().pull(PullWorkerOptions::new().app_worker_id("bad/id")),
        WorkerOptions::new().pull(PullWorkerOptions::new().app_worker_id("RegisteredFlow")),
        WorkerOptions::new()
            .pull(PullWorkerOptions::new())
            .bind_address("127.0.0.1:8803"),
        WorkerOptions::new()
            .pull(PullWorkerOptions::new())
            .worker_target(WorkerTarget::pull()),
        WorkerOptions::new().worker_target(WorkerTarget::pull()),
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
        zero_capacity_error.to_string().contains(
            "pull Worker task limit, idle stream count, and drain timeout must be positive"
        )
    );
    assert!(
        Worker::try_new(
            registry,
            cache,
            WorkerOptions::new().pull(PullWorkerOptions::new())
        )
        .is_ok()
    );
    assert!(WorkerTarget::pull().headless(true).validate().is_err());
}
