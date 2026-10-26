// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use dexzd_protocol::dexzd::v1::NodeLease;
use prost::Message;
use s3mock::{
    ChecksumAlgorithm, Durability, EtagMode, PutObjectRequest, Store, StoreConfig,
    UploadChecksumExpectations,
};
use s3mock_server::ServerConfig;
use tokio::net::TcpListener;
use tokio::sync::{Notify, watch};

use super::{LeaseExpiryResponse, start_server};
use crate::config::{Config, CredentialSource};

const BUCKET: &str = "dex-serve";
const NODE_ID: &str = "serve-node";
const STARTUP_BUDGET: Duration = Duration::from_secs(10);
const RELEASE_BUDGET: Duration = Duration::from_secs(5);
/// Well below the 30-second S3 request timeout that a stalled startup check would wait for.
const CANCELLATION_BUDGET: Duration = Duration::from_secs(5);

/// A serve start that waits for a lease another process holds can be aborted and frees its port.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn an_aborted_serve_start_waiting_for_a_held_lease_releases_the_grpc_listener() {
    let directory = tempfile::tempdir().expect("test directory");
    let store = Store::open(&StoreConfig {
        root_directory: directory.path().join("s3"),
        durability: Durability::Relaxed,
        etag_mode: EtagMode::Md5,
    })
    .expect("the object store opens");
    store
        .create_bucket(BUCKET, false)
        .expect("the bucket is created");
    put_lease_held_by_another_process(&store);
    let object_store_listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("the object store listener binds");
    let object_store = s3mock_server::start_with_config(
        Arc::new(store),
        object_store_listener,
        ServerConfig {
            data_directory: directory.path().join("s3"),
            ..ServerConfig::default()
        },
    )
    .expect("the object store serves");

    let api_listen = std::net::TcpListener::bind("127.0.0.1:0")
        .and_then(|listener| listener.local_addr())
        .expect("a free loopback port exists");
    let mut configuration = Config::default();
    configuration.cluster.node_id = NODE_ID.to_owned();
    configuration.cluster.partition_count = 16;
    configuration.object_store.endpoint = format!("http://{}", object_store.local_address());
    configuration.object_store.region = "us-east-1".to_owned();
    configuration.object_store.bucket = BUCKET.to_owned();
    configuration.object_store.force_path_style = true;
    configuration.object_store.credentials_source = CredentialSource::Anonymous;
    configuration.local_storage.directory = directory.path().join("store");
    configuration.api.listen = api_listen;

    let (_cancellation_sender, mut cancellation) = watch::channel(false);
    let waiting = tokio::spawn(async move {
        start_server(
            &configuration,
            None,
            &mut cancellation,
            LeaseExpiryResponse::StopNodeOnly,
        )
        .await
        .map(|started| started.is_some())
    });
    tokio::time::timeout(STARTUP_BUDGET, async {
        while tokio::net::TcpStream::connect(api_listen).await.is_err() {
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .expect("the waiting serve start opens its early listener");
    assert!(
        !waiting.is_finished(),
        "the serve start must wait for the held lease"
    );
    waiting.abort();
    let Err(cancelled) = waiting.await else {
        panic!("the waiting serve start completed");
    };
    assert!(cancelled.is_cancelled());

    let rebound_listener = tokio::time::timeout(RELEASE_BUDGET, async {
        loop {
            match TcpListener::bind(api_listen).await {
                Ok(listener) => break listener,
                Err(error) if error.kind() == std::io::ErrorKind::AddrInUse => {
                    tokio::time::sleep(Duration::from_millis(20)).await;
                }
                Err(error) => panic!("the aborted serve listener could not rebind: {error}"),
            }
        }
    })
    .await
    .expect("the aborted serve start releases its gRPC listener");
    drop(rebound_listener);
    object_store.shutdown().await;
}

/// A signal while the object store startup check waits on a silent endpoint cancels startup.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_signal_during_the_object_store_startup_check_cancels_startup_promptly() {
    let directory = tempfile::tempdir().expect("test directory");
    let silent_object_store = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("the silent object store listener binds");
    let object_store_address = silent_object_store
        .local_addr()
        .expect("the silent object store listener has an address");
    let connection_accepted = Arc::new(Notify::new());
    let accepting = tokio::spawn({
        let connection_accepted = connection_accepted.clone();
        async move {
            // Connections stay open without a response, like a stalled object store.
            let mut open_connections = Vec::new();
            while let Ok((connection, _)) = silent_object_store.accept().await {
                open_connections.push(connection);
                connection_accepted.notify_one();
            }
        }
    });

    let api_listen = std::net::TcpListener::bind("127.0.0.1:0")
        .and_then(|listener| listener.local_addr())
        .expect("a free loopback port exists");
    let mut configuration = Config::default();
    configuration.cluster.node_id = NODE_ID.to_owned();
    configuration.cluster.partition_count = 16;
    configuration.object_store.endpoint = format!("http://{object_store_address}");
    configuration.object_store.region = "us-east-1".to_owned();
    configuration.object_store.bucket = BUCKET.to_owned();
    configuration.object_store.force_path_style = true;
    configuration.object_store.credentials_source = CredentialSource::Anonymous;
    configuration.local_storage.directory = directory.path().join("store");
    configuration.api.listen = api_listen;

    let (cancellation_sender, mut cancellation) = watch::channel(false);
    let starting = tokio::spawn(async move {
        start_server(
            &configuration,
            None,
            &mut cancellation,
            LeaseExpiryResponse::StopNodeOnly,
        )
        .await
        .map(|started| started.is_some())
    });
    tokio::time::timeout(STARTUP_BUDGET, connection_accepted.notified())
        .await
        .expect("startup connects to the object store");
    cancellation_sender.send_replace(true);
    let started = tokio::time::timeout(CANCELLATION_BUDGET, starting)
        .await
        .expect("startup stops before the object store request times out")
        .expect("the start task completes");
    assert!(
        !started.expect("a cancelled startup is not a failure"),
        "a cancelled startup does not serve"
    );
    accepting.abort();
}

/// Stores a lease of `NODE_ID` that another process holds for the next minute.
fn put_lease_held_by_another_process(store: &Store) {
    let now_micros = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("the clock is after the Unix epoch")
        .as_micros();
    let lease = NodeLease {
        node_id: NODE_ID.to_owned(),
        incarnation: 1,
        epoch: 1,
        holder_process_id: "held-by-another-process".to_owned(),
        expires_at_unix_micros: i64::try_from(now_micros).expect("time fits i64") + 60_000_000,
        duration_micros: 60_000_000,
        version: 1,
    };
    let lease_key = format!("lease/{NODE_ID}");
    let mut writer = store
        .begin_put(BUCKET, &lease_key, ChecksumAlgorithm::Crc32)
        .expect("the lease upload starts");
    writer
        .write_all(&lease.encode_to_vec())
        .expect("the lease is written");
    let staged = writer
        .finish(&UploadChecksumExpectations::default())
        .expect("the lease upload finishes");
    store
        .commit_put(BUCKET, &lease_key, staged, PutObjectRequest::default())
        .expect("the lease is stored");
}
