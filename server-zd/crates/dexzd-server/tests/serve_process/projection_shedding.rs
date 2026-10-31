// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;
use std::fs;

use dexzd_protocol::dex::{Kv, StopFlowRequest, StopType};

use super::metrics::{sample_sum, sample_with_label, scrape};
use super::*;

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn postgres_tiny_projection_queue_sheds_by_priority_and_preserves_purges() {
    if let Ok(database_url) = std::env::var("DEX_ZD_POSTGRES_URL") {
        run_tiny_projection_queue(&database_url, true, 1_000_000).await;
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn paradedb_tiny_projection_queue_sheds_by_priority_and_preserves_purges() {
    if let Ok(database_url) = std::env::var("DEX_ZD_PARADEDB_URL") {
        run_tiny_projection_queue(&database_url, false, 1_000_000).await;
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn postgres_projection_staging_overflow_diffs_and_converges() {
    if let Ok(database_url) = std::env::var("DEX_ZD_POSTGRES_URL") {
        run_tiny_projection_queue(&database_url, true, 1).await;
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn paradedb_projection_staging_overflow_diffs_and_converges() {
    if let Ok(database_url) = std::env::var("DEX_ZD_PARADEDB_URL") {
        run_tiny_projection_queue(&database_url, false, 1).await;
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn postgres_idle_projection_checkpoints_1024_partitions_within_shutdown_budget() {
    let Ok(database_url) = std::env::var("DEX_ZD_POSTGRES_URL") else {
        return;
    };
    let schema = SqlVisibilitySchema::create_for_dialect(&database_url, true).await;
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let directory = TempDir::new().expect("test directory exists");
    let address = free_loopback_address();
    let config_path = directory.path().join("idle-checkpoints.yaml");
    ServeConfig {
        primary_endpoint: &primary.endpoint(),
        hard_delete_endpoint: None,
        listen: address,
        local_directory: &directory.path().join("store"),
        lease_duration: "3s",
        fail_if_held: false,
        parade_db: Some(SqlVisibilitySettings {
            is_postgres: true,
            connection_string: &database_url,
            schema: schema.name(),
        }),
    }
    .write(&config_path);
    let configuration = fs::read_to_string(&config_path)
        .expect("read configuration")
        .replace("partitionCount: 16", "partitionCount: 1024");
    fs::write(&config_path, configuration).expect("write 1024-partition configuration");
    let mut server = ServeProcess::spawn(
        &config_path,
        address,
        directory.path().join("idle-checkpoints.log"),
    );
    let mut client = FlowServiceClient::new(server.wait_until_serving().await);
    start_idle_flow(&mut client, 0).await;
    let mut poll = tokio::time::interval(Duration::from_millis(20));
    tokio::time::timeout(SEARCH_BUDGET, async {
        loop {
            poll.tick().await;
            match search_shedding_flows(&mut client).await {
                Ok(entries) if entries.len() == 1 => break,
                Ok(_) => {}
                Err(status) if status.code() == tonic::Code::Unavailable => {}
                Err(status) => panic!("search failed: {status}; {}", server.log()),
            }
        }
    })
    .await
    .expect("startup projection catches up");
    let status = server.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", server.log());
    schema
        .assert_idle_checkpoints_match_local_store(&directory.path().join("store"), &server.log())
        .await;
    primary.shutdown().await;
    schema.drop_schema().await;
}

async fn run_tiny_projection_queue(database_url: &str, is_postgres: bool, repair_limit: usize) {
    let schema = SqlVisibilitySchema::create_for_dialect(database_url, is_postgres).await;
    let mut proxy = SqlVisibilityTcpProxy::start(database_url).await;
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let directory = TempDir::new().expect("test directory exists");
    let address = free_loopback_address();
    let metrics_address = free_loopback_address();
    let config_path = directory.path().join("shedding.yaml");
    ServeConfig {
        primary_endpoint: &primary.endpoint(),
        hard_delete_endpoint: None,
        listen: address,
        local_directory: &directory.path().join("store"),
        lease_duration: "3s",
        fail_if_held: false,
        parade_db: Some(SqlVisibilitySettings {
            is_postgres,
            connection_string: &proxy.connection_string(database_url),
            schema: schema.name(),
        }),
    }
    .write_with_closed_flow_retention(&config_path, "30s");
    let configuration = fs::read_to_string(&config_path).expect("test configuration exists");
    let configuration = configuration.replace(
        "search:\n",
        &format!("search:\n  projectionQueue:\n    softFrames: 4\n    hardFrames: 8\n    softBytes: 67108864\n    hardBytes: 134217728\n    maxRepairEntries: {repair_limit}\n    repairBatchRuns: 2\n"),
    );
    fs::write(
        &config_path,
        format!("{configuration}metrics:\n  listen: '{metrics_address}'\n"),
    )
    .expect("test configuration is writable");
    let mut server =
        ServeProcess::spawn(&config_path, address, directory.path().join("shedding.log"));
    let channel = server.wait_until_serving().await;
    let mut admin =
        dexzd_protocol::dex::admin_service_client::AdminServiceClient::new(channel.clone());
    let mut client = FlowServiceClient::new(channel);
    let mut committed_versions = BTreeMap::new();
    for run_number in 0..32 {
        start_idle_flow(&mut client, run_number).await;
        remember_committed_version(&mut client, &mut committed_versions, run_number, None).await;
        search_shedding_flows(&mut client)
            .await
            .expect("initial start is projected");
    }
    proxy.cut().await;
    for run_number in 0..16 {
        for version in 1..=5 {
            write_indexed_attribute(&mut client, run_number, version).await;
            remember_committed_version(
                &mut client,
                &mut committed_versions,
                run_number,
                Some(version),
            )
            .await;
        }
    }
    for run_number in 16..32 {
        terminate_flow(&mut client, run_number).await;
        remember_committed_version(&mut client, &mut committed_versions, run_number, None).await;
    }
    for run_number in 32..48 {
        start_idle_flow(&mut client, run_number).await;
        remember_committed_version(&mut client, &mut committed_versions, run_number, None).await;
        for version in 1..=5 {
            write_indexed_attribute(&mut client, run_number, version).await;
            remember_committed_version(
                &mut client,
                &mut committed_versions,
                run_number,
                Some(version),
            )
            .await;
        }
    }
    let exposed = scrape(metrics_address, "/metrics").await;
    let coalesced_before_restart =
        sample_sum(&exposed, "dexzd_search_projection_coalesced_changes_total");
    for reason in ["intermediate", "close", "start"] {
        assert!(
            sample_with_label(
                &exposed,
                "dexzd_search_projection_dropped_updates_total",
                "reason",
                reason
            ) > 0.0,
            "{reason}: {exposed}"
        );
    }
    if repair_limit == 1 {
        assert!(sample_sum(&exposed, "dexzd_search_projection_repair_overflow_total") > 0.0);
        assert_eq!(
            sample_sum(&exposed, "dexzd_search_projection_rebuild_requests_total"),
            0.0
        );
    } else {
        assert_eq!(
            sample_sum(&exposed, "dexzd_search_projection_rebuild_requests_total"),
            0.0
        );
    }
    if repair_limit == 1 {
        let killed = server.kill().await;
        assert!(!killed.success());
        fs::remove_dir_all(directory.path().join("store/search-pending"))
            .expect("remove unsynced local pending sidecar");
        proxy.restore();
        server = ServeProcess::spawn(
            &config_path,
            address,
            directory.path().join("after-crash.log"),
        );
        let channel = server.wait_until_serving().await;
        admin = dexzd_protocol::dex::admin_service_client::AdminServiceClient::new(channel.clone());
        client = FlowServiceClient::new(channel);
    } else {
        proxy.restore();
    }
    let entries = wait_for_exact_projection(&mut client, &server, &committed_versions, &[]).await;
    assert_committed_versions(&entries, &committed_versions);
    wait_for_repairs_to_drain(metrics_address).await;
    if repair_limit > 1 {
        let exposed = scrape(metrics_address, "/metrics").await;
        for reason in ["intermediate", "close", "start"] {
            assert!(
                sample_with_label(
                    &exposed,
                    "dexzd_search_projection_repaired_runs_total",
                    "reason",
                    reason
                ) > 0.0,
                "{reason}: {exposed}"
            );
        }
    }

    proxy.cut().await;
    for run_number in 0..2 {
        terminate_flow(&mut client, run_number).await;
    }
    let purged_runs: Vec<_> = (0..2).chain(16..32).collect();
    let mut poll = tokio::time::interval(Duration::from_millis(100));
    tokio::time::timeout(Duration::from_secs(90), async {
        loop {
            poll.tick().await;
            let mut has_purged_all = true;
            for &run_number in &purged_runs {
                match client
                    .get_flow_summary(GetFlowSummaryRequest {
                        flow_id: flow_id(run_number),
                        ..Default::default()
                    })
                    .await
                {
                    Err(status) if status.code() == tonic::Code::NotFound => {}
                    Ok(_) => has_purged_all = false,
                    Err(status) => panic!("purge read failed: {status}"),
                }
            }
            if has_purged_all {
                break;
            }
        }
    })
    .await
    .expect("closed runs purge while SQL is unavailable");
    assert_eq!(
        sample_sum(
            &scrape(metrics_address, "/metrics").await,
            "dexzd_search_projection_rebuild_requests_total"
        ),
        0.0
    );
    if repair_limit == 1 {
        assert!(!server.kill().await.success());
        fs::remove_dir_all(directory.path().join("store/search-pending"))
            .expect("remove local pending state before purge restart");
        proxy.restore();
        server = ServeProcess::spawn(
            &config_path,
            address,
            directory.path().join("after-purge-crash.log"),
        );
        let channel = server.wait_until_serving().await;
        admin = dexzd_protocol::dex::admin_service_client::AdminServiceClient::new(channel.clone());
        client = FlowServiceClient::new(channel);
    } else {
        proxy.restore();
    }
    let entries =
        wait_for_exact_projection(&mut client, &server, &committed_versions, &purged_runs).await;
    assert!(
        entries.iter().all(|entry| purged_runs
            .iter()
            .all(|&run_number| entry.flow_id != flow_id(run_number))),
        "every purged run is absent"
    );
    assert_committed_versions(&entries, &committed_versions);
    write_indexed_attribute(&mut client, 32, 6).await;
    remember_committed_version(&mut client, &mut committed_versions, 32, Some(6)).await;
    let entries =
        wait_for_exact_projection(&mut client, &server, &committed_versions, &purged_runs).await;
    assert!(
        entries.iter().any(|entry| entry
            == committed_versions[&flow_id(32)]
                .last()
                .expect("the later committed version was recorded")),
        "a later write projects a previously absent run normally"
    );
    assert_committed_versions(&entries, &committed_versions);
    let exposed = scrape(metrics_address, "/metrics").await;
    assert!(
        coalesced_before_restart
            + sample_sum(&exposed, "dexzd_search_projection_coalesced_changes_total")
            > 0.0
    );
    assert_eq!(
        sample_sum(&exposed, "dexzd_search_skipped_documents_total"),
        0.0
    );
    assert_eq!(
        sample_sum(&exposed, "dexzd_search_projection_rebuild_requests_total"),
        0.0
    );
    let divergent_partition = schema.diverge_run_partition_progress(&flow_id(32)).await;
    write_indexed_attribute(&mut client, 32, 7).await;
    remember_committed_version(&mut client, &mut committed_versions, 32, Some(7)).await;
    let mut poll = tokio::time::interval(Duration::from_millis(50));
    tokio::time::timeout(SEARCH_BUDGET, async {
        loop {
            poll.tick().await;
            let status = admin
                .get_search_projection_status(())
                .await
                .expect("read divergence marker")
                .into_inner();
            if status.partitions.iter().any(|partition| {
                partition.partition_id == divergent_partition
                    && partition
                        .rebuild_reasons
                        .iter()
                        .any(|reason| reason == "divergence")
            }) {
                break;
            }
        }
    })
    .await
    .expect("divergence marks the partition without automatic rebuilding");
    assert_eq!(
        sample_sum(
            &scrape(metrics_address, "/metrics").await,
            "dexzd_search_projection_rebuild_requests_total"
        ),
        0.0
    );
    let scheduled = admin
        .rebuild_search_projection(dexzd_protocol::dex::RebuildSearchProjectionRequest {
            all_marked_partitions: true,
            ..Default::default()
        })
        .await
        .expect("explicit marked-partition rebuilding is allowed without authentication")
        .into_inner();
    assert!(
        scheduled
            .scheduled_partition_ids
            .contains(&divergent_partition)
    );
    let scheduled_count = scheduled.scheduled_partition_ids.len();
    let mut poll = tokio::time::interval(Duration::from_millis(50));
    tokio::time::timeout(SEARCH_BUDGET, async {
        loop {
            poll.tick().await;
            let status = admin
                .get_search_projection_status(())
                .await
                .expect("read manual rebuild status")
                .into_inner();
            if status
                .partitions
                .iter()
                .all(|partition| !partition.is_scheduled && !partition.is_in_progress)
            {
                break;
            }
        }
    })
    .await
    .expect("explicit rebuild finishes asynchronously");
    assert_eq!(
        sample_sum(
            &scrape(metrics_address, "/metrics").await,
            "dexzd_search_projection_rebuild_requests_total"
        ),
        scheduled_count as f64
    );
    let entries =
        wait_for_exact_projection(&mut client, &server, &committed_versions, &purged_runs).await;
    assert_committed_versions(&entries, &committed_versions);
    wait_for_repairs_to_drain(metrics_address).await;
    let status = server.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", server.log());
    schema
        .assert_idle_checkpoints_match_local_store(&directory.path().join("store"), &server.log())
        .await;
    proxy.shutdown().await;
    primary.shutdown().await;
    schema.drop_schema().await;
}

async fn start_idle_flow(client: &mut FlowServiceClient<Channel>, run_number: usize) {
    client
        .start_flow(StartFlowRequest {
            flow_id: flow_id(run_number),
            flow_type: FLOW_TYPE.to_owned(),
            start_step_type: "IdleStep".to_owned(),
            flow_start_options: Some(FlowStartOptions {
                flow_config_override: Some(FlowConfig {
                    worker_target: Some(WorkerTarget { is_pull_mode: true }),
                    ..Default::default()
                }),
                ..Default::default()
            }),
            request_id: format!("start-{run_number}"),
            ..Default::default()
        })
        .await
        .expect("start commits during overload");
}

async fn write_indexed_attribute(
    client: &mut FlowServiceClient<Channel>,
    run_number: usize,
    version: usize,
) {
    client
        .set_attributes(SetAttributesRequest {
            flow_id: flow_id(run_number),
            attributes: vec![AttributeWrite {
                key: "revision".to_owned(),
                value: Some(string_value(&version.to_string())),
                index_config: Some(IndexConfig {
                    enable: true,
                    r#type: IndexType::Keyword as i32,
                    ..Default::default()
                }),
                ..Default::default()
            }],
            request_id: format!("update-{run_number}-{version}"),
            ..Default::default()
        })
        .await
        .expect("Attribute update commits during overload");
}

async fn terminate_flow(client: &mut FlowServiceClient<Channel>, run_number: usize) {
    client
        .stop_flow(StopFlowRequest {
            flow_id: flow_id(run_number),
            stop_type: StopType::Terminate as i32,
            ..Default::default()
        })
        .await
        .expect("close commits during overload");
}

async fn wait_for_exact_projection(
    client: &mut FlowServiceClient<Channel>,
    server: &ServeProcess,
    versions: &BTreeMap<String, Vec<SearchFlowsResponseEntry>>,
    purged_runs: &[usize],
) -> Vec<SearchFlowsResponseEntry> {
    let mut poll = tokio::time::interval(Duration::from_millis(100));
    tokio::time::timeout(Duration::from_secs(60), async {
        loop {
            poll.tick().await;
            match search_shedding_flows(client).await {
                Ok(entries)
                    if entries.len() == versions.len() - purged_runs.len()
                        && entries
                            .iter()
                            .all(|entry| versions[&entry.flow_id].last() == Some(entry))
                        && entries.iter().all(|entry| {
                            purged_runs
                                .iter()
                                .all(|&run_number| entry.flow_id != flow_id(run_number))
                        }) =>
                {
                    return entries;
                }
                Ok(_) => {}
                Err(status) if status.code() == tonic::Code::Unavailable => {}
                Err(status) => panic!("projection recovery failed: {status}; {}", server.log()),
            }
        }
    })
    .await
    .unwrap_or_else(|error| panic!("projection did not drain: {error}; {}", server.log()))
}

async fn wait_for_repairs_to_drain(address: std::net::SocketAddr) {
    let mut poll = tokio::time::interval(Duration::from_millis(20));
    tokio::time::timeout(Duration::from_secs(30), async {
        loop {
            poll.tick().await;
            if sample_sum(
                &scrape(address, "/metrics").await,
                "dexzd_search_projection_repair_pending",
            ) == 0.0
            {
                return;
            }
        }
    })
    .await
    .expect("repair pending gauge drains after exact convergence");
}

async fn remember_committed_version(
    client: &mut FlowServiceClient<Channel>,
    versions: &mut BTreeMap<String, Vec<SearchFlowsResponseEntry>>,
    run_number: usize,
    revision: Option<usize>,
) {
    let summary = client
        .get_flow_summary(GetFlowSummaryRequest {
            flow_id: flow_id(run_number),
            ..Default::default()
        })
        .await
        .expect("committed summary is readable")
        .into_inner();
    let execution_id = summary.flow_execution_id.expect("committed run has an ID");
    versions
        .entry(execution_id.flow_id.clone())
        .or_default()
        .push(SearchFlowsResponseEntry {
            flow_id: execution_id.flow_id,
            run_id: execution_id.run_id,
            flow_type: summary.flow_type,
            flow_status: summary.flow_status,
            start_time: summary.start_time,
            close_time: summary.close_time,
            indexed_attributes: std::iter::once(Kv {
                key: "FlowType".to_owned(),
                value: Some(string_value(FLOW_TYPE)),
            })
            .chain(revision.into_iter().map(|revision| Kv {
                key: "revision".to_owned(),
                value: Some(string_value(&revision.to_string())),
            }))
            .collect(),
        });
}

fn assert_committed_versions(
    entries: &[SearchFlowsResponseEntry],
    versions: &BTreeMap<String, Vec<SearchFlowsResponseEntry>>,
) {
    for entry in entries {
        assert!(
            versions[&entry.flow_id].contains(entry),
            "projected row must equal a complete committed version: {entry:?}"
        );
    }
}

fn flow_id(run_number: usize) -> String {
    format!("projection-shedding-{run_number}")
}

async fn search_shedding_flows(
    client: &mut FlowServiceClient<Channel>,
) -> Result<Vec<SearchFlowsResponseEntry>, tonic::Status> {
    client
        .search_flows(SearchFlowsRequest {
            query: format!("FlowType = '{FLOW_TYPE}'"),
            page_size: 100,
            ..Default::default()
        })
        .await
        .map(|response| response.into_inner().flow_runs)
}
