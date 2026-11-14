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
    BlobCache, BlobCacheConfig, Client, ClientOptions, PullWorkerOptions, Registry, SdkError,
    SdkResult, StartFlowOptions, Worker, WorkerOptions,
};

use crate::support::{flow_id, server_address};
use crate::task_queue_workflow::{TaskQueueLabelFlow, TaskQueueParentFlow};

const BLUE_TASK_QUEUE: &str = "rust-blue";
const GREEN_TASK_QUEUE: &str = "rust-green";
const DEFAULT_WORKER_LABEL: &str = "default";
const PENDING_CHECK: Duration = Duration::from_secs(3);
const COMPLETION_WAIT: Duration = Duration::from_secs(30);

struct LabeledWorker {
    worker: Arc<Worker>,
    thread: JoinHandle<SdkResult<()>>,
}

impl LabeledWorker {
    fn stop(self) {
        self.worker.stop();
        self.thread
            .join()
            .expect("join task queue Worker")
            .expect("task queue Worker stopped");
    }
}

struct TaskQueueTestEnvironment {
    cache: Arc<BlobCache>,
    runtime: tokio::runtime::Runtime,
    client: Client,
}

impl TaskQueueTestEnvironment {
    fn start_worker(&self, task_queue: &str, worker_label: &str) -> LabeledWorker {
        let worker = Arc::new(
            Worker::try_new(
                registry(worker_label),
                Arc::clone(&self.cache),
                WorkerOptions::new()
                    .server_address(server_address())
                    .pull(PullWorkerOptions::new().task_queue(task_queue)),
            )
            .expect("task queue Worker"),
        );
        assert_eq!(worker.task_queue(), task_queue);
        let running = Arc::clone(&worker);
        let thread = thread::spawn(move || running.start());
        self.runtime
            .block_on(async {
                tokio::time::timeout(Duration::from_secs(30), worker.wait_pull_registered()).await
            })
            .expect("registration deadline")
            .expect("task queue Worker registered");
        assert!(worker.pull_registration_state().is_ready);
        LabeledWorker { worker, thread }
    }

    fn start_label_flow(&self, task_queue: &str) -> String {
        let flow_id = flow_id("rust-task-queue-label");
        self.client
            .start_flow_with_options(
                &TaskQueueLabelFlow::new(DEFAULT_WORKER_LABEL),
                &flow_id,
                (),
                StartFlowOptions::new().task_queue(task_queue),
            )
            .expect("start label Flow");
        flow_id
    }

    fn start_parent_flow(&self, task_queue: &str, sub_flow_task_queue: &str) -> String {
        let flow_id = flow_id("rust-task-queue-parent");
        self.client
            .start_flow_with_options(
                &TaskQueueParentFlow::new(DEFAULT_WORKER_LABEL),
                &flow_id,
                sub_flow_task_queue.to_string(),
                StartFlowOptions::new().task_queue(task_queue),
            )
            .expect("start parent Flow");
        flow_id
    }

    /// Asserts that no running Worker completes the Flow within [`PENDING_CHECK`].
    fn assert_pending(&self, flow_id: &str) {
        match self
            .client
            .wait_for_flow_with_timeout(flow_id, PENDING_CHECK)
        {
            Err(SdkError::LongPollTimeout { .. }) => {}
            Ok(result) => panic!(
                "Flow {flow_id} ran on a Worker of another task queue: {:?}",
                result.single_output::<String>()
            ),
            Err(error) => panic!("wait for pending Flow {flow_id}: {error}"),
        }
    }

    fn output(&self, flow_id: &str) -> String {
        self.client
            .wait_for_flow_with_timeout(flow_id, COMPLETION_WAIT)
            .and_then(|result| result.single_output())
            .expect("Flow output")
    }
}

fn registry(worker_label: &str) -> Registry {
    Registry::new()
        .register(TaskQueueLabelFlow::new(worker_label))
        .and_then(|registry| registry.register(TaskQueueParentFlow::new(worker_label)))
        .expect("register task queue Flows")
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
    let environment = TaskQueueTestEnvironment {
        runtime: tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("wait runtime"),
        client: Client::try_new(
            registry(DEFAULT_WORKER_LABEL),
            Arc::clone(&cache),
            ClientOptions::new().server_address(server_address()),
        )
        .expect("Client"),
        cache: Arc::clone(&cache),
    };

    // Only Workers of other task queues run while each Flow starts; it must wait for its own.
    let green = environment.start_worker(GREEN_TASK_QUEUE, "green");
    let default = environment.start_worker("", DEFAULT_WORKER_LABEL);
    let blue_flow = environment.start_label_flow(BLUE_TASK_QUEUE);
    environment.assert_pending(&blue_flow);
    let blue = environment.start_worker(BLUE_TASK_QUEUE, "blue");
    assert_eq!(environment.output(&blue_flow), "blue");

    default.stop();
    let default_flow = environment.start_label_flow("");
    let default_alias_flow = environment.start_label_flow("dex.default");
    let default_sub_flow_parent = environment.start_parent_flow(BLUE_TASK_QUEUE, "dex.default");
    environment.assert_pending(&default_flow);
    environment.assert_pending(&default_alias_flow);
    environment.assert_pending(&default_sub_flow_parent);
    let default = environment.start_worker("", DEFAULT_WORKER_LABEL);
    assert_eq!(environment.output(&default_flow), DEFAULT_WORKER_LABEL);
    assert_eq!(
        environment.output(&default_alias_flow),
        DEFAULT_WORKER_LABEL
    );
    assert_eq!(environment.output(&default_sub_flow_parent), "blue>default");

    green.stop();
    let green_flow = environment.start_label_flow(GREEN_TASK_QUEUE);
    let green_sub_flow_parent = environment.start_parent_flow(BLUE_TASK_QUEUE, GREEN_TASK_QUEUE);
    environment.assert_pending(&green_flow);
    environment.assert_pending(&green_sub_flow_parent);
    let green = environment.start_worker(GREEN_TASK_QUEUE, "green");
    assert_eq!(environment.output(&green_flow), "green");
    assert_eq!(environment.output(&green_sub_flow_parent), "blue>green");

    // Isolation is proven above, so a SubFlow off its parent's task queue would get another label.
    let inherited_parent = environment.start_parent_flow(BLUE_TASK_QUEUE, "");
    assert_eq!(environment.output(&inherited_parent), "blue>blue");
    let default_parent = environment.start_parent_flow("", "");
    assert_eq!(environment.output(&default_parent), "default>default");

    for labeled in [blue, green, default] {
        labeled.stop();
    }
    cache.close().expect("close cache");
}
