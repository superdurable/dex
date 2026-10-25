// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::worker_task_service_client::WorkerTaskServiceClient;
use dexzd_protocol::dex::{GetFlowSummaryRequest, PollWorkerTaskRequest};
use dexzd_server::{DevCommandOptions, start_dev_server};
use s3mock::{Durability, EtagMode, Store, StoreConfig};
use s3mock_server::SignatureVerification;
use tempfile::TempDir;
use tonic::Code;
use tonic::transport::Channel;
use tonic_health::pb::HealthCheckRequest;
use tonic_health::pb::health_check_response::ServingStatus;
use tonic_health::pb::health_client::HealthClient;

use super::harness::{S3Emulator, ServeConfig, ServeProcess, free_loopback_address};
use super::{PRIMARY_BUCKET, SHUTDOWN_BUDGET};

const LEASE_DURATION: &str = "2s";
const RESPONSE_BUDGET: Duration = Duration::from_secs(1);
const HEALTH_SERVICE_NAMES: [&str; 3] = ["", "dex.FlowService", "dex.WorkerTaskService"];

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn readiness_attempt_times_out_when_tcp_accepts_without_grpc() {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("the stalled listener binds");
    let address = listener.local_addr().expect("the listener has an address");
    let stalled = tokio::spawn(async move {
        let (stream, _) = listener.accept().await.expect("the connection is accepted");
        std::future::pending::<()>().await;
        drop(stream);
    });
    let readiness = tokio::time::timeout(
        Duration::from_secs(3),
        ServeProcess::readiness_channel(address),
    )
    .await
    .expect("a stalled gRPC readiness call finishes within three seconds");
    assert!(readiness.is_none());
    stalled.abort();
    assert!(
        stalled
            .await
            .expect_err("the stalled server task is cancelled")
            .is_cancelled()
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn sigterm_stops_a_standby_waiting_for_a_live_holder() {
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let primary_endpoint = primary.endpoint();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let first_address = free_loopback_address();
    let first_config = directory.path().join("first.yaml");
    ServeConfig {
        primary_endpoint: &primary_endpoint,
        hard_delete_endpoint: None,
        listen: first_address,
        local_directory: &directory.path().join("first-store"),
        lease_duration: "10s",
        fail_if_held: true,
        parade_db: None,
    }
    .write(&first_config);
    let mut first = ServeProcess::spawn(
        &first_config,
        first_address,
        directory.path().join("first.log"),
    );
    first.wait_until_serving().await;

    let standby_address = free_loopback_address();
    let standby_config = directory.path().join("standby.yaml");
    ServeConfig {
        primary_endpoint: &primary_endpoint,
        hard_delete_endpoint: None,
        listen: standby_address,
        local_directory: &directory.path().join("standby-store"),
        lease_duration: "10s",
        fail_if_held: false,
        parade_db: None,
    }
    .write(&standby_config);
    let mut standby = ServeProcess::spawn(
        &standby_config,
        standby_address,
        directory.path().join("standby.log"),
    );
    standby
        .wait_for_log_line(
            "another process holds the node lease; waiting",
            Duration::from_secs(10),
        )
        .await;
    let status = standby.terminate(Duration::from_secs(3)).await;
    assert_eq!(status.code(), Some(0), "{}", standby.log());
    assert!(
        first.is_serving_within(RESPONSE_BUDGET).await,
        "lease holder stopped serving:\n{}",
        first.log()
    );
    assert_eq!(
        first.terminate(SHUTDOWN_BUDGET).await.code(),
        Some(0),
        "{}",
        first.log()
    );
    primary.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn cancelled_dev_start_releases_the_grpc_listener() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    // dev takes a held lease over at once, so a silent ParadeDB keeps startup waiting instead.
    let silent_parade_db = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("the silent ParadeDB listener binds");
    let parade_db_address = silent_parade_db
        .local_addr()
        .expect("the silent ParadeDB listener has an address");
    let silent_parade_db_task = tokio::spawn(async move {
        let mut connections = Vec::new();
        loop {
            let (stream, _) = silent_parade_db
                .accept()
                .await
                .expect("the ParadeDB connection is accepted");
            connections.push(stream);
        }
    });
    let config_path = directory.path().join("overrides.yaml");
    std::fs::write(
        &config_path,
        format!(
            "search:\n  backend: paradedb\n  paradeDbConnectionString: \"host=127.0.0.1 port={} user=dex dbname=dex connect_timeout=60\"\n",
            parade_db_address.port()
        ),
    )
    .expect("the dev overrides are written");

    let waiting_options = DevCommandOptions {
        data_dir: directory.path().join("data"),
        listen: free_loopback_address(),
        log_dir: None,
        config: Some(config_path),
        attribute_store_config: None,
    };
    let waiting_address = waiting_options.listen;
    let waiting = tokio::spawn(async move { start_dev_server(&waiting_options).await });
    tokio::time::timeout(Duration::from_secs(10), async {
        loop {
            if let Ok(Ok(stream)) = tokio::time::timeout(
                RESPONSE_BUDGET,
                tokio::net::TcpStream::connect(waiting_address),
            )
            .await
            {
                drop(stream);
                break;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .expect("the waiting dev server opens its early listener");
    assert!(
        !waiting.is_finished(),
        "the dev server must still wait for ParadeDB"
    );
    waiting.abort();
    let cancelled = match waiting.await {
        Ok(_) => panic!("the waiting start completed"),
        Err(error) => error,
    };
    assert!(cancelled.is_cancelled());
    tokio::time::timeout(Duration::from_secs(5), async {
        loop {
            match Store::open(&StoreConfig {
                root_directory: directory.path().join("data").join("s3"),
                durability: Durability::Relaxed,
                etag_mode: EtagMode::Md5,
            }) {
                Ok(store) => {
                    drop(store);
                    break;
                }
                Err(_) => tokio::time::sleep(Duration::from_millis(20)).await,
            }
        }
    })
    .await
    .expect("the cancelled dev server releases its embedded object store");
    silent_parade_db_task.abort();

    let rebound_listener = tokio::time::timeout(Duration::from_secs(5), async {
        loop {
            match tokio::net::TcpListener::bind(waiting_address).await {
                Ok(listener) => break listener,
                Err(error) if error.kind() == std::io::ErrorKind::AddrInUse => {
                    tokio::time::sleep(Duration::from_millis(20)).await;
                }
                Err(error) => panic!("the cancelled dev server listener could not rebind: {error}"),
            }
        }
    })
    .await
    .expect("the cancelled dev server releases its gRPC listener within five seconds");
    drop(rebound_listener);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn waiting_standby_reports_not_serving_then_serves_after_takeover() {
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let primary_endpoint = primary.endpoint();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let first_address = free_loopback_address();
    let first_config = directory.path().join("first.yaml");
    ServeConfig {
        primary_endpoint: &primary_endpoint,
        hard_delete_endpoint: None,
        listen: first_address,
        local_directory: &directory.path().join("first-store"),
        lease_duration: LEASE_DURATION,
        fail_if_held: true,
        parade_db: None,
    }
    .write(&first_config);
    let mut first = ServeProcess::spawn(
        &first_config,
        first_address,
        directory.path().join("first.log"),
    );
    first.wait_until_serving().await;

    let second_address = free_loopback_address();
    let second_config = directory.path().join("second.yaml");
    ServeConfig {
        primary_endpoint: &primary_endpoint,
        hard_delete_endpoint: None,
        listen: second_address,
        local_directory: &directory.path().join("second-store"),
        lease_duration: LEASE_DURATION,
        fail_if_held: false,
        parade_db: None,
    }
    .write(&second_config);
    let mut second = ServeProcess::spawn(
        &second_config,
        second_address,
        directory.path().join("second.log"),
    );
    let channel = tokio::time::timeout(Duration::from_secs(10), async {
        loop {
            if let Ok(Ok(channel)) = tokio::time::timeout(
                RESPONSE_BUDGET,
                Channel::from_shared(format!("http://{second_address}"))
                    .expect("the standby address is a valid URI")
                    .connect(),
            )
            .await
            {
                break channel;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .unwrap_or_else(|_| panic!("the standby did not accept connections:\n{}", second.log()));

    let mut watches = Vec::new();
    for service in HEALTH_SERVICE_NAMES {
        let request = HealthCheckRequest {
            service: service.to_owned(),
        };
        let mut health = HealthClient::new(channel.clone());
        let check = tokio::time::timeout(RESPONSE_BUDGET, health.check(request.clone()))
            .await
            .unwrap_or_else(|_| panic!("standby health Check timed out:\n{}", second.log()))
            .unwrap_or_else(|error| {
                panic!("standby health Check failed: {error}\n{}", second.log())
            });
        assert_eq!(check.get_ref().status, ServingStatus::NotServing as i32);
        let mut watch = tokio::time::timeout(RESPONSE_BUDGET, health.watch(request))
            .await
            .expect("the standby health Watch responds within one second")
            .expect("the standby health Watch succeeds")
            .into_inner();
        let initial = tokio::time::timeout(RESPONSE_BUDGET, watch.message())
            .await
            .expect("the standby health Watch sends within one second")
            .expect("the standby health Watch remains open")
            .expect("the standby health Watch sends a status");
        assert_eq!(initial.status, ServingStatus::NotServing as i32);
        watches.push(watch);
    }
    let flow_error = tokio::time::timeout(
        RESPONSE_BUDGET,
        FlowServiceClient::new(channel.clone()).get_flow_summary(GetFlowSummaryRequest {
            flow_id: "standby-check".to_owned(),
            run_id: String::new(),
        }),
    )
    .await
    .expect("the standby FlowService responds within one second")
    .expect_err("the standby FlowService rejects requests");
    assert_eq!(flow_error.code(), Code::Unavailable);
    assert!(flow_error.message().contains("node is starting"));
    let worker_error = tokio::time::timeout(
        RESPONSE_BUDGET,
        WorkerTaskServiceClient::new(channel.clone())
            .poll_worker_task(tokio_stream::empty::<PollWorkerTaskRequest>()),
    )
    .await
    .expect("the standby WorkerTaskService responds within one second")
    .expect_err("the standby WorkerTaskService rejects requests");
    assert_eq!(worker_error.code(), Code::Unavailable);
    assert!(worker_error.message().contains("node is starting"));

    let first_status = first.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(first_status.code(), Some(0), "{}", first.log());
    let serving_channel = second.wait_until_serving().await;
    for mut watch in watches {
        let serving = tokio::time::timeout(RESPONSE_BUDGET, watch.message())
            .await
            .expect("the health Watch updates within one second")
            .expect("the health Watch remains open")
            .expect("the health Watch sends a status");
        assert_eq!(serving.status, ServingStatus::Serving as i32);
    }
    tokio::time::timeout(
        Duration::from_secs(5),
        FlowServiceClient::new(serving_channel).search_flows(
            dexzd_protocol::dex::SearchFlowsRequest {
                query: "FlowType = 'StandbyHealthFlow'".to_owned(),
                page_size: 10,
                ..Default::default()
            },
        ),
    )
    .await
    .unwrap_or_else(|_| panic!("active FlowService timed out:\n{}", second.log()))
    .unwrap_or_else(|error| panic!("active FlowService failed: {error}\n{}", second.log()));
    let second_status = second.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(second_status.code(), Some(0), "{}", second.log());
    primary.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn failed_startup_closes_the_health_listener() {
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let primary_endpoint = primary.endpoint();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let first_address = free_loopback_address();
    let first_config = directory.path().join("first.yaml");
    ServeConfig {
        primary_endpoint: &primary_endpoint,
        hard_delete_endpoint: None,
        listen: first_address,
        local_directory: &directory.path().join("first-store"),
        lease_duration: "10s",
        fail_if_held: true,
        parade_db: None,
    }
    .write(&first_config);
    let mut first = ServeProcess::spawn(
        &first_config,
        first_address,
        directory.path().join("first.log"),
    );
    first.wait_until_serving().await;

    let second_address = free_loopback_address();
    let second_config = directory.path().join("second.yaml");
    ServeConfig {
        primary_endpoint: &primary_endpoint,
        hard_delete_endpoint: None,
        listen: second_address,
        local_directory: &directory.path().join("second-store"),
        lease_duration: "10s",
        fail_if_held: true,
        parade_db: None,
    }
    .write(&second_config);
    let mut second = ServeProcess::spawn(
        &second_config,
        second_address,
        directory.path().join("second.log"),
    );
    let status = second.wait_for_exit(Duration::from_secs(10)).await;
    assert_eq!(status.code(), Some(1), "{}", second.log());
    assert!(second.log().contains("lease is held"), "{}", second.log());
    let connection = tokio::time::timeout(
        RESPONSE_BUDGET,
        tokio::net::TcpStream::connect(second_address),
    )
    .await
    .unwrap_or_else(|_| panic!("failed startup health connect timed out:\n{}", second.log()));
    assert!(
        connection.is_err(),
        "failed startup left the health listener open:\n{}",
        second.log()
    );
    assert_eq!(
        first.terminate(SHUTDOWN_BUDGET).await.code(),
        Some(0),
        "{}",
        first.log()
    );
    primary.shutdown().await;
}
