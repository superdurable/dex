// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::sync::Arc;
use std::thread::{self, JoinHandle};
use std::time::Duration;

use dex_sdk::{
    BlobCache, BlobCacheConfig, Client, ClientOptions, PullWorkerOptions, Registry, SdkResult,
    StartFlowOptions, Worker, WorkerOptions,
};

use crate::support::{flow_id, server_address};
use crate::task_queue_workflow::{TaskQueueLabelFlow, TaskQueueParentFlow};

const DEFAULT_WORKER_LABEL: &str = "default";

struct LabeledWorker {
    worker: Arc<Worker>,
    thread: JoinHandle<SdkResult<()>>,
}

fn registry(worker_label: &str) -> Registry {
    Registry::new()
        .register(TaskQueueLabelFlow::new(worker_label))
        .and_then(|registry| registry.register(TaskQueueParentFlow::new(worker_label)))
        .expect("register task queue Flows")
}

fn start_worker(
    cache: &Arc<BlobCache>,
    runtime: &tokio::runtime::Runtime,
    task_queue: &str,
    worker_label: &str,
) -> LabeledWorker {
    let worker = Arc::new(
        Worker::try_new(
            registry(worker_label),
            Arc::clone(cache),
            WorkerOptions::new()
                .server_address(server_address())
                .pull(PullWorkerOptions::new().task_queue(task_queue)),
        )
        .expect("task queue Worker"),
    );
    let running = Arc::clone(&worker);
    let thread = thread::spawn(move || running.start());
    runtime
        .block_on(async {
            tokio::time::timeout(Duration::from_secs(30), worker.wait_pull_registered()).await
        })
        .expect("registration deadline")
        .expect("task queue Worker registered");
    let state = worker.pull_registration_state();
    assert!(state.is_ready);
    assert!(
        state
            .queues
            .iter()
            .all(|queue| queue.task_queue == task_queue)
    );
    LabeledWorker { worker, thread }
}

fn run_label_flow(client: &Client, options: StartFlowOptions) -> String {
    let flow_id = flow_id("rust-task-queue-label");
    client
        .start_flow_with_options(
            &TaskQueueLabelFlow::new(DEFAULT_WORKER_LABEL),
            &flow_id,
            (),
            options,
        )
        .expect("start label Flow");
    client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
        .and_then(|result| result.single_output())
        .expect("label Flow output")
}

fn run_parent_flow(client: &Client, task_queue: &str, sub_flow_task_queue: &str) -> String {
    let flow_id = flow_id("rust-task-queue-parent");
    client
        .start_flow_with_options(
            &TaskQueueParentFlow::new(DEFAULT_WORKER_LABEL),
            &flow_id,
            sub_flow_task_queue.to_string(),
            StartFlowOptions::new().task_queue(task_queue),
        )
        .expect("start parent Flow");
    client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
        .and_then(|result| result.single_output())
        .expect("parent Flow output")
}

#[test]
#[ignore = "requires dexcli dev"]
fn task_queues_route_one_flow_type_to_separate_workers() {
    let directory = tempfile::tempdir().expect("cache directory");
    let cache = Arc::new(
        BlobCache::open(
            BlobCacheConfig::new(directory.path(), 64 * 1024 * 1024, 0).expect("cache config"),
        )
        .expect("cache"),
    );
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("wait runtime");
    let workers = [
        start_worker(&cache, &runtime, "rust-blue", "blue"),
        start_worker(&cache, &runtime, "rust-green", "green"),
        start_worker(&cache, &runtime, "", DEFAULT_WORKER_LABEL),
    ];
    let client = Client::try_new(
        registry(DEFAULT_WORKER_LABEL),
        Arc::clone(&cache),
        ClientOptions::new().server_address(server_address()),
    )
    .expect("Client");

    for _ in 0..3 {
        assert_eq!(
            run_label_flow(&client, StartFlowOptions::new().task_queue("rust-blue")),
            "blue"
        );
        assert_eq!(
            run_label_flow(&client, StartFlowOptions::new().task_queue("rust-green")),
            "green"
        );
        assert_eq!(
            run_label_flow(&client, StartFlowOptions::new()),
            DEFAULT_WORKER_LABEL
        );
    }
    assert_eq!(
        run_label_flow(&client, StartFlowOptions::new().task_queue("dex.default")),
        DEFAULT_WORKER_LABEL
    );
    assert_eq!(run_parent_flow(&client, "rust-blue", ""), "blue>blue");
    assert_eq!(
        run_parent_flow(&client, "rust-blue", "dex.default"),
        "blue>default"
    );
    assert_eq!(
        run_parent_flow(&client, "rust-blue", "rust-green"),
        "blue>green"
    );
    assert_eq!(run_parent_flow(&client, "", ""), "default>default");

    for labeled in &workers {
        labeled.worker.stop();
    }
    for labeled in workers {
        labeled
            .thread
            .join()
            .expect("join task queue Worker")
            .expect("task queue Worker stopped");
    }
    cache.close().expect("close cache");
}
