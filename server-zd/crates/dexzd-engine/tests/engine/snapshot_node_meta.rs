// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::{Arc, Mutex};
use std::time::Duration;

use dexzd_engine::{
    EngineLocalStore, EngineNode, EngineNodeDependencies, EngineRuntimeHooks, EngineServices,
    HardDeleteUploadLedger, LocalStoreConfig, NodeMetaUploadLedger, SystemEngineIdSource,
};
use dexzd_protocol::dexzd::v1::PendingHardDeleteUpload;
use dexzd_storage::{LocalReadView, RocksDbStoreOptions};

use crate::harness::{CountingApplier, RecordingRuntime, TestCluster, TestNode};

/// How long the first snapshot file upload is held.
const SNAPSHOT_UPLOAD_DELAY: Duration = Duration::from_secs(5);

/// How long a ledger write may take while the upload is held.
const LEDGER_WRITE_BUDGET: Duration = Duration::from_secs(2);

/// Starts a node and returns the NodeMeta services that the server wires into its upload ledger.
async fn start_node_with_node_meta_services(
    cluster: &mut TestCluster,
    config: &dexzd_engine::EngineNodeConfig,
) -> (TestNode, Arc<EngineLocalStore>, Arc<Mutex<()>>) {
    let captured = Arc::new(Mutex::new(None));
    let factory_captured = captured.clone();
    let applier = Arc::new(CountingApplier::default());
    let runtime = Arc::new(RecordingRuntime::default());
    let factory_runtime = runtime.clone();
    let node = EngineNode::start(
        config,
        EngineNodeDependencies {
            metrics: Arc::new(dexzd_metrics::NodeMetrics::unregistered()),
            object_store: cluster.object_store.clone(),
            clock: cluster.clock.clone(),
            applier: applier.clone(),
            id_source: Arc::new(SystemEngineIdSource),
            lease_validity_deadline: dexzd_cluster_metadata::LeaseValidityDeadline::disarmed(),
        },
        move |services: EngineServices| {
            *factory_captured.lock().expect("capture lock") =
                Some((services.local_store, services.node_meta_lock));
            EngineRuntimeHooks {
                committed_commands: factory_runtime.clone(),
                rebuilder: factory_runtime,
            }
        },
    )
    .await
    .expect("node starts");
    let cluster_metadata = dexzd_cluster_metadata::bootstrap_or_validate(
        cluster.object_store.as_ref(),
        cluster.clock.as_ref(),
        config.cluster,
    )
    .await
    .expect("cluster exists");
    cluster.hasher = cluster_metadata.partition_hasher;
    let (local_store, node_meta_lock) = captured
        .lock()
        .expect("capture lock")
        .take()
        .expect("the runtime factory ran");
    let node = TestNode {
        node,
        applier,
        runtime,
        hasher: cluster.hasher.clone(),
    };
    (node, local_store, node_meta_lock)
}

/// A StartFlow with a hard-delete Attribute records its upload in NodeMeta on a runtime worker.
/// Runs in real time: the upload runs on the blocking pool.
#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn hard_delete_upload_records_do_not_wait_for_an_in_flight_snapshot_upload() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let mut cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::RocksDb {
        directory: directory.path().join("store"),
        options: RocksDbStoreOptions::default(),
    });
    let (first, local_store, node_meta_lock) =
        start_node_with_node_meta_services(&mut cluster, &config).await;
    let ledger = Arc::new(NodeMetaUploadLedger::new(
        local_store.clone(),
        node_meta_lock,
    ));
    first.commit_prompt("snapshot-node-meta-flow", 2).await;
    cluster
        .faults
        .delay_next_put_under("snapshot/", SNAPSHOT_UPLOAD_DELAY);
    let upload = PendingHardDeleteUpload {
        object_key: "hard-delete/0000000000000001/0000000000000002/run/object".to_owned(),
        uploaded_at_unix_micros: 7,
    };

    let in_flight_snapshot = first.node.take_snapshot();
    let ledger_write = async {
        cluster.faults.wait_until_delayed_operation_entered().await;
        let ledger = ledger.clone();
        let upload = upload.clone();
        tokio::time::timeout(
            LEDGER_WRITE_BUDGET,
            tokio::task::spawn_blocking(move || ledger.record_pending_uploads(&[upload])),
        )
        .await
    };
    let (snapshot, ledger_write) = tokio::join!(in_flight_snapshot, ledger_write);

    ledger_write
        .expect("the ledger write does not wait for the snapshot upload")
        .expect("the ledger write task completes")
        .expect("the ledger write succeeds");
    let snapshot = snapshot.expect("the snapshot succeeds");
    assert_eq!(snapshot.snapshot_number, 1);
    let node_meta = local_store
        .node_meta()
        .expect("NodeMeta reads")
        .expect("NodeMeta exists");
    assert_eq!(node_meta.last_snapshot_number, 1);
    assert_eq!(
        node_meta.pending_hard_delete_uploads,
        vec![upload],
        "the snapshot's NodeMeta update keeps the upload recorded during the upload"
    );
    first.node.shutdown().await.expect("lease released");
}
