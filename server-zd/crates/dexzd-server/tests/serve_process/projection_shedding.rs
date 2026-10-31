// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;
use std::fs;

use dexzd_protocol::dex::{StopFlowRequest, StopType};

use super::metrics::{sample_sum, scrape};
use super::*;

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn postgres_tiny_projection_queue_preserves_starts_closes_and_purges() {
    if let Ok(database_url) = std::env::var("DEX_ZD_POSTGRES_URL") {
        run_tiny_projection_queue(&database_url, true).await;
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn paradedb_tiny_projection_queue_preserves_starts_closes_and_purges() {
    if let Ok(database_url) = std::env::var("DEX_ZD_PARADEDB_URL") {
        run_tiny_projection_queue(&database_url, false).await;
    }
}

async fn run_tiny_projection_queue(database_url: &str, is_postgres: bool) {
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
        lease_duration: HELD_LEASE_DURATION,
        fail_if_held: true,
        parade_db: Some(SqlVisibilitySettings {
            is_postgres,
            connection_string: &proxy.connection_string(database_url),
            schema: schema.name(),
        }),
    }
    .write_with_closed_flow_retention(&config_path, "5s");
    let configuration = fs::read_to_string(&config_path).expect("test configuration exists");
    let configuration = configuration.replace(
        "search:\n",
        "search:\n  projectionQueue:\n    softFrames: 4\n    hardFrames: 8\n    softBytes: 67108864\n    hardBytes: 134217728\n",
    );
    fs::write(
        &config_path,
        format!("{configuration}metrics:\n  listen: '{metrics_address}'\n"),
    )
    .expect("test configuration is writable");
    let mut server =
        ServeProcess::spawn(&config_path, address, directory.path().join("shedding.log"));
    let channel = server.wait_until_serving().await;
    let mut client = FlowServiceClient::new(channel);
    for run_number in 0..16 {
        start_idle_flow(&mut client, run_number).await;
        search_shedding_flows(&mut client)
            .await
            .expect("initial start is projected");
    }
    proxy.cut().await;
    for run_number in 0..16 {
        for version in 1..=5 {
            write_indexed_attribute(&mut client, run_number, version).await;
        }
    }
    for run_number in 16..32 {
        start_idle_flow(&mut client, run_number).await;
        for version in 1..=5 {
            write_indexed_attribute(&mut client, run_number, version).await;
        }
    }
    for run_number in 0..2 {
        terminate_flow(&mut client, run_number).await;
    }
    let mut poll = tokio::time::interval(Duration::from_millis(100));
    tokio::time::timeout(Duration::from_secs(30), async {
        loop {
            poll.tick().await;
            let mut has_purged_both = true;
            for run_number in 0..2 {
                match client
                    .get_flow_summary(GetFlowSummaryRequest {
                        flow_id: flow_id(run_number),
                        ..Default::default()
                    })
                    .await
                {
                    Err(status) if status.code() == tonic::Code::NotFound => {}
                    Ok(_) => has_purged_both = false,
                    Err(status) => panic!("purge read failed: {status}"),
                }
            }
            if has_purged_both {
                break;
            }
        }
    })
    .await
    .expect("closed runs purge while SQL is unavailable");
    proxy.restore();
    wait_for_projected_set(&mut client, &server).await;
    for run_number in 2..8 {
        terminate_flow(&mut client, run_number).await;
    }
    let entries = search_shedding_flows(&mut client)
        .await
        .expect("closed documents project");
    assert_eq!(entries.len(), 30, "purged runs remain absent");
    let by_flow_id: BTreeMap<_, _> = entries
        .iter()
        .map(|entry| (entry.flow_id.clone(), entry))
        .collect();
    for run_number in 2..32 {
        let entry = by_flow_id
            .get(&flow_id(run_number))
            .expect("every retained run is projected");
        let expected_status = if run_number < 8 {
            FlowStatus::Terminated
        } else {
            FlowStatus::Running
        };
        assert_eq!(entry.flow_status, expected_status as i32);
        if run_number < 8 {
            let actual = entry
                .indexed_attributes
                .iter()
                .find(|attribute| attribute.key == "revision")
                .and_then(|attribute| attribute.value.as_ref());
            assert_eq!(
                actual,
                Some(&string_value("5")),
                "closed run has its final document"
            );
        }
    }
    let exposed = scrape(metrics_address, "/metrics").await;
    for metric in [
        "dexzd_search_projection_coalesced_changes_total",
        "dexzd_search_projection_dropped_updates_total",
        "dexzd_search_projection_rebuild_requests_total",
    ] {
        assert!(sample_sum(&exposed, metric) > 0.0, "{metric}: {exposed}");
    }
    assert_eq!(
        sample_sum(&exposed, "dexzd_search_skipped_documents_total"),
        0.0
    );
    let status = server.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", server.log());
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

async fn wait_for_projected_set(client: &mut FlowServiceClient<Channel>, server: &ServeProcess) {
    let mut poll = tokio::time::interval(Duration::from_millis(100));
    tokio::time::timeout(Duration::from_secs(60), async {
        loop {
            poll.tick().await;
            match search_shedding_flows(client).await {
                Ok(entries)
                    if entries.len() == 30
                        && entries.iter().all(|entry| {
                            entry.flow_id != flow_id(0) && entry.flow_id != flow_id(1)
                        }) =>
                {
                    break;
                }
                Ok(_) => {}
                Err(status) if status.code() == tonic::Code::Unavailable => {}
                Err(status) => panic!("projection recovery failed: {status}; {}", server.log()),
            }
        }
    })
    .await
    .unwrap_or_else(|error| panic!("projected set did not converge: {error}; {}", server.log()));
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
