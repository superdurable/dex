// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::*;

#[tokio::test]
async fn reserve_pressure_retains_only_declining_queue_until_timer_expires() {
    let channel = outbound_task_channel(Endpoint::from_static("http://127.0.0.1:1"), None);
    let (cache, _directory) = test_cache();
    let registry = Registry::new()
        .register(ReserveCapacityFlow)
        .expect("register Flow");
    let dispatcher = WorkerDispatcher::new(
        registry,
        ValueHydrator::new(FlowServiceClient::new(channel.clone()), cache),
        crate::server_protocol::NegotiatedServerProtocolVersion::default(),
    );
    let mut options = PullWorkerOptions::new()
        .app_worker_id("pressure-worker")
        .max_concurrent_tasks(3)
        .reserved_rpc_tasks(1);
    options.reserve_pressure_linger = Some(Duration::from_millis(200));
    let worker = PullWorker::new(&options, &dispatcher, channel).expect("pull Worker");
    let stream_slot = |queue: &str, is_busy| StreamSlot {
        queue: queue.to_string(),
        is_busy,
        is_registered: true,
        is_retiring: false,
        is_draining: false,
        retirement: Arc::new(Notify::new()),
    };
    let shared = Arc::new(PullShared {
        registration: Arc::clone(&worker.registration),
        state: Mutex::new(PullState {
            streams: HashMap::from([
                (1, stream_slot("pressure-worker", false)),
                (2, stream_slot("ReserveCapacityFlow", false)),
                (3, stream_slot("ReserveCapacityFlow", false)),
                (4, stream_slot("ReserveCapacityFlow", true)),
                (5, stream_slot("ReserveCapacityFlow", true)),
            ]),
            last_reserve_decline: HashMap::new(),
            busy_tasks: 2,
            next_stream_id: 6,
            is_stopping: false,
            logged_unsupported: false,
        }),
        notify: Notify::new(),
        max_concurrent_tasks: 3,
        reserved_rpc_tasks: 1,
        reserved_rpc_declines: Arc::new(AtomicU64::new(0)),
        idle_streams_per_queue: 1,
        reserve_pressure_linger: worker.reserve_pressure_linger,
    });
    let idle_count = |queue: &str| {
        shared
            .state
            .lock()
            .expect("pull state lock")
            .streams
            .values()
            .filter(|slot| {
                slot.queue == queue
                    && slot.is_registered
                    && !slot.is_busy
                    && !slot.is_retiring
                    && !slot.is_draining
            })
            .count()
    };
    let step = PollWorkerTask {
        request: Some(TaskRequest::Execute(Default::default())),
        ..Default::default()
    };
    assert!(!shared.try_accept(2, Some(&step)));
    let (_stop, stop_receiver) = watch::channel(StopSignal {
        phase: 0,
        deadline: None,
    });
    let mut streams = JoinSet::new();
    worker.reconcile(&shared, &stop_receiver, &mut streams);
    assert_eq!(2, idle_count("ReserveCapacityFlow"));
    assert_eq!(1, idle_count("pressure-worker"));
    assert!(shared.release(4));
    worker.reconcile(&shared, &stop_receiver, &mut streams);
    assert_eq!(2, idle_count("ReserveCapacityFlow"));
    assert_eq!(1, idle_count("pressure-worker"));
    timeout(
        Duration::from_secs(1),
        shared.wait_for_reserve_pressure_expiry(),
    )
    .await
    .expect("reserve pressure timer expired");
    worker.reconcile(&shared, &stop_receiver, &mut streams);
    assert_eq!(1, idle_count("ReserveCapacityFlow"));
    assert_eq!(1, idle_count("pressure-worker"));
}
