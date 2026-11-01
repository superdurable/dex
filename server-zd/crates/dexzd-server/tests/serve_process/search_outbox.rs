// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The SearchOutbox projection lags under an outage without losing writes and survives crashes,
//! takeovers, and backend switches.

use std::collections::BTreeMap;
use std::fs;
use std::net::SocketAddr;
use std::path::Path;

use dexzd_keyspace::row_keys::{partition_index_range, search_projection_mode_key};
use dexzd_keyspace::{ColumnFamily, IndexKind, PartitionId};
use dexzd_protocol::dexzd::v1::SearchProjectionModeRecord;
use dexzd_storage::{LocalReadView, RocksDbLocalStore, RocksDbStoreOptions, ScanDirection};
use prost::Message;
use tokio::io::{AsyncReadExt, AsyncWriteExt};

use super::*;

/// The serve configuration's partition count.
const PARTITION_COUNT: u16 = 16;
const OUTBOX_FLOW_COUNT: usize = 24;
const LOAD_LOG_LINE: &str = "loading the partition into an empty SQL visibility projection";
const COUNTER_ATTRIBUTE: &str = "outboxCounter";

/// A server process of one test step and the files it uses.
struct OutboxNode<'a> {
    directory: &'a TempDir,
    primary_endpoint: &'a str,
}

/// Search settings of one process: SQL visibility, or builtin when `sql` is `None`.
struct OutboxSearch<'a> {
    sql: Option<SqlVisibilitySettings<'a>>,
    ack_interval: &'a str,
}

impl OutboxNode<'_> {
    /// Starts a process on `store` with a two-second lease that a successor waits out.
    async fn start(
        &self,
        name: &str,
        store: &str,
        search: OutboxSearch<'_>,
    ) -> (ServeProcess, Channel, SocketAddr) {
        let address = free_loopback_address();
        let metrics_address = free_loopback_address();
        let path = self.directory.path().join(format!("{name}.yaml"));
        let is_sql = search.sql.is_some();
        ServeConfig {
            primary_endpoint: self.primary_endpoint,
            hard_delete_endpoint: None,
            listen: address,
            local_directory: &self.directory.path().join(store),
            lease_duration: "2s",
            fail_if_held: false,
            parade_db: search.sql,
        }
        .write(&path);
        let mut contents = fs::read_to_string(&path).expect("the serve configuration is readable");
        if is_sql {
            contents.push_str(&format!("  outboxAckInterval: {}\n", search.ack_interval));
        }
        contents.push_str(&format!("metrics:\n  listen: '{metrics_address}'\n"));
        fs::write(&path, contents).expect("the serve configuration is written");
        let mut process = ServeProcess::spawn(
            &path,
            address,
            self.directory.path().join(format!("{name}.log")),
        );
        let channel = process.wait_until_serving().await;
        (process, channel, metrics_address)
    }

    fn store_path(&self, store: &str) -> std::path::PathBuf {
        self.directory.path().join(store)
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn paradedb_search_outbox_survives_an_outage_a_crash_and_a_takeover() {
    let Ok(database_url) = std::env::var("DEX_ZD_PARADEDB_URL") else {
        return;
    };
    run_outage_crash_and_takeover_for_test(&database_url, false).await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn postgres_search_outbox_survives_an_outage_a_crash_and_a_takeover() {
    let Ok(database_url) = std::env::var("DEX_ZD_POSTGRES_URL") else {
        return;
    };
    run_outage_crash_and_takeover_for_test(&database_url, true).await;
}

async fn run_outage_crash_and_takeover_for_test(database_url: &str, is_postgres: bool) {
    let schema = SqlVisibilitySchema::create_for_dialect(database_url, is_postgres).await;
    let mut proxy = SqlVisibilityTcpProxy::start(database_url).await;
    let proxied_url = proxy.connection_string(database_url);
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let primary_endpoint = primary.endpoint();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let node = OutboxNode {
        directory: &directory,
        primary_endpoint: &primary_endpoint,
    };
    let sql = |connection_string| SqlVisibilitySettings {
        is_postgres,
        connection_string,
        schema: schema.name(),
    };
    let flow_ids: Vec<String> = (0..OUTBOX_FLOW_COUNT)
        .map(|number| format!("outbox-flow-{number:02}"))
        .collect();

    // Acks of later entries wait an hour, so a crash finds projected entries still queued.
    let (mut first, channel, first_metrics) = node
        .start(
            "first",
            "store",
            OutboxSearch {
                sql: Some(sql(&proxied_url)),
                ack_interval: "1h",
            },
        )
        .await;
    let mut flow_client = FlowServiceClient::new(channel);
    for flow_id in &flow_ids {
        start_idle_flow(&mut flow_client, flow_id).await;
        set_counter(&mut flow_client, flow_id, 0).await;
    }
    assert_counters_now(&mut flow_client, &flow_ids, 0).await;

    proxy.cut().await;
    for round in 1..=20 {
        for flow_id in &flow_ids {
            set_counter(&mut flow_client, flow_id, round).await;
        }
    }
    let during_outage = search_counters(&mut flow_client).await;
    assert_eq!(
        during_outage.as_ref().map_err(tonic::Status::code),
        Err(tonic::Code::Unavailable),
        "SearchFlows never returns stale rows: {during_outage:?}"
    );
    let outage_metrics = scrape_metrics(first_metrics).await;
    assert!(
        metric_value(
            &outage_metrics,
            "dexzd_search_outbox_oldest_entry_age_seconds"
        ) > 0.0,
        "{outage_metrics}"
    );
    proxy.restore();
    search_until_counters(&mut flow_client, &flow_ids, 20, &first).await;
    set_counter(&mut flow_client, &flow_ids[0], 21).await;
    let after_write = search_counters(&mut flow_client)
        .await
        .expect("SearchFlows succeeds once SQL visibility is reachable");
    assert_eq!(after_write.get(&flow_ids[0]), Some(&21), "read-your-writes");
    let caught_up_metrics = scrape_metrics(first_metrics).await;
    let projected_entries = metric_value(
        &caught_up_metrics,
        "dexzd_search_outbox_projected_entries_total",
    );
    let projected_runs = metric_value(
        &caught_up_metrics,
        "dexzd_search_outbox_projected_runs_total",
    );
    assert!(
        projected_runs < projected_entries,
        "batches coalesce hot runs: {projected_runs} rows for {projected_entries} entries"
    );
    assert_eq!(
        metric_value(&caught_up_metrics, "dexzd_search_outbox_entries"),
        0.0,
        "{caught_up_metrics}"
    );
    assert_eq!(
        first.log().matches(LOAD_LOG_LINE).count(),
        usize::from(PARTITION_COUNT),
        "each partition loads once and nothing rebuilds: {}",
        first.log()
    );
    for flow_id in &flow_ids {
        set_counter(&mut flow_client, flow_id, 22).await;
    }
    assert_counters_now(&mut flow_client, &flow_ids, 22).await;
    let killed = first.kill().await;
    assert!(!killed.success(), "SIGKILL stops the first process");

    // The restarted process projects the queued entries again and acks them.
    let (mut second, channel, second_metrics) = node
        .start(
            "second",
            "store",
            OutboxSearch {
                sql: Some(sql(&proxied_url)),
                ack_interval: "100ms",
            },
        )
        .await;
    let mut flow_client = FlowServiceClient::new(channel);
    assert_counters_now(&mut flow_client, &flow_ids, 22).await;
    wait_for_metric(
        second_metrics,
        "dexzd_search_outbox_ack_proposals_total{outcome=\"ok\"}",
        &second,
    )
    .await;
    assert!(!second.log().contains(LOAD_LOG_LINE), "{}", second.log());
    let status = second.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", second.log());

    // A recovered local store keeps projecting; hour-long acks leave projected entries queued.
    let (mut third, channel, _) = node
        .start(
            "third",
            "recovered-store",
            OutboxSearch {
                sql: Some(sql(&proxied_url)),
                ack_interval: "1h",
            },
        )
        .await;
    let mut flow_client = FlowServiceClient::new(channel);
    for flow_id in &flow_ids {
        set_counter(&mut flow_client, flow_id, 23).await;
    }
    assert_counters_now(&mut flow_client, &flow_ids, 23).await;
    for flow_id in &flow_ids {
        set_counter(&mut flow_client, flow_id, 24).await;
    }
    let sql_entries = assert_counters_now(&mut flow_client, &flow_ids, 24).await;
    assert!(!third.log().contains(LOAD_LOG_LINE), "{}", third.log());
    let status = third.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", third.log());
    let store_path = node.store_path("recovered-store");
    assert!(
        count_outbox_entries(&store_path) > 0,
        "projected entries wait for their ack"
    );

    // Builtin search on the same store clears the queue and reads exactly the projected state.
    let (mut fourth, channel, _) = node
        .start(
            "fourth",
            "recovered-store",
            OutboxSearch {
                sql: None,
                ack_interval: "100ms",
            },
        )
        .await;
    let mut flow_client = FlowServiceClient::new(channel);
    let builtin_entries = search_entries(&mut flow_client)
        .await
        .expect("builtin SearchFlows succeeds");
    assert_eq!(
        comparable_entries(&builtin_entries),
        comparable_entries(&sql_entries),
        "SQL visibility equals committed state"
    );
    let status = fourth.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", fourth.log());
    assert_eq!(count_outbox_entries(&store_path), 0);
    assert!(
        stored_modes(&store_path)
            .iter()
            .all(|is_enabled| !is_enabled)
    );

    proxy.shutdown().await;
    primary.shutdown().await;
    schema.drop_schema().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn paradedb_search_outbox_loads_a_builtin_cluster_during_writes() {
    let Ok(database_url) = std::env::var("DEX_ZD_PARADEDB_URL") else {
        return;
    };
    run_builtin_to_sql_switch_for_test(&database_url, false).await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn postgres_search_outbox_loads_a_builtin_cluster_during_writes() {
    let Ok(database_url) = std::env::var("DEX_ZD_POSTGRES_URL") else {
        return;
    };
    run_builtin_to_sql_switch_for_test(&database_url, true).await;
}

async fn run_builtin_to_sql_switch_for_test(database_url: &str, is_postgres: bool) {
    let schema = SqlVisibilitySchema::create_for_dialect(database_url, is_postgres).await;
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let primary_endpoint = primary.endpoint();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let node = OutboxNode {
        directory: &directory,
        primary_endpoint: &primary_endpoint,
    };
    let early_flows: Vec<String> = (0..OUTBOX_FLOW_COUNT)
        .map(|number| format!("builtin-flow-{number:02}"))
        .collect();
    let late_flows: Vec<String> = (0..OUTBOX_FLOW_COUNT)
        .map(|number| format!("switched-flow-{number:02}"))
        .collect();

    let (mut builtin, channel, _) = node
        .start(
            "builtin",
            "store",
            OutboxSearch {
                sql: None,
                ack_interval: "100ms",
            },
        )
        .await;
    let mut flow_client = FlowServiceClient::new(channel);
    for flow_id in &early_flows {
        start_idle_flow(&mut flow_client, flow_id).await;
        set_counter(&mut flow_client, flow_id, 1).await;
    }
    let status = builtin.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", builtin.log());
    let store_path = node.store_path("store");
    assert_eq!(
        count_outbox_entries(&store_path),
        0,
        "builtin appends nothing"
    );
    assert!(
        stored_modes(&store_path)
            .iter()
            .all(|is_enabled| !is_enabled)
    );

    let (mut switched, channel, _) = node
        .start(
            "switched",
            "store",
            OutboxSearch {
                sql: Some(SqlVisibilitySettings {
                    is_postgres,
                    connection_string: database_url,
                    schema: schema.name(),
                }),
                ack_interval: "100ms",
            },
        )
        .await;
    let mut flow_client = FlowServiceClient::new(channel);
    for flow_id in &late_flows {
        start_idle_flow(&mut flow_client, flow_id).await;
        set_counter(&mut flow_client, flow_id, 2).await;
    }
    for flow_id in &early_flows {
        set_counter(&mut flow_client, flow_id, 3).await;
    }
    assert_counters_now(&mut flow_client, &late_flows, 2).await;
    assert_counters_now(&mut flow_client, &early_flows, 3).await;
    assert_eq!(
        switched.log().matches(LOAD_LOG_LINE).count(),
        usize::from(PARTITION_COUNT),
        "{}",
        switched.log()
    );
    let status = switched.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", switched.log());
    assert!(
        stored_modes(&store_path)
            .iter()
            .all(|is_enabled| *is_enabled)
    );
    primary.shutdown().await;
    schema.drop_schema().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn paradedb_search_outbox_deletes_purged_runs_and_acks_their_entries() {
    let Ok(database_url) = std::env::var("DEX_ZD_PARADEDB_URL") else {
        return;
    };
    run_purge_for_test(&database_url, false).await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn postgres_search_outbox_deletes_purged_runs_and_acks_their_entries() {
    let Ok(database_url) = std::env::var("DEX_ZD_POSTGRES_URL") else {
        return;
    };
    run_purge_for_test(&database_url, true).await;
}

async fn run_purge_for_test(database_url: &str, is_postgres: bool) {
    let schema = SqlVisibilitySchema::create_for_dialect(database_url, is_postgres).await;
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let primary_endpoint = primary.endpoint();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let address = free_loopback_address();
    let metrics_address = free_loopback_address();
    let config_path = directory.path().join("purge.yaml");
    ServeConfig {
        primary_endpoint: &primary_endpoint,
        hard_delete_endpoint: None,
        listen: address,
        local_directory: &directory.path().join("store"),
        lease_duration: HELD_LEASE_DURATION,
        fail_if_held: true,
        parade_db: Some(SqlVisibilitySettings {
            is_postgres,
            connection_string: database_url,
            schema: schema.name(),
        }),
    }
    .write_with_closed_flow_retention(&config_path, "2s");
    let mut contents =
        fs::read_to_string(&config_path).expect("the serve configuration is readable");
    contents.push_str(&format!("metrics:\n  listen: '{metrics_address}'\n"));
    fs::write(&config_path, contents).expect("the serve configuration is written");
    let mut server = ServeProcess::spawn(&config_path, address, directory.path().join("purge.log"));
    let channel = server.wait_until_serving().await;
    let worker = start_greeting_worker(channel.clone()).await;
    let mut flow_client = FlowServiceClient::new(channel);
    let run_id = run_greeting_flow(&mut flow_client, "purged-flow").await;
    let listed = search_entries(&mut flow_client)
        .await
        .expect("SearchFlows sees the completed Flow without polling");
    assert_eq!(
        listed.get("purged-flow").map(|entry| entry.run_id.as_str()),
        Some(run_id.as_str())
    );
    worker.stop().await;

    let deadline = tokio::time::Instant::now() + Duration::from_secs(60);
    loop {
        match search_entries(&mut flow_client).await {
            Ok(entries) if !entries.contains_key("purged-flow") => break,
            Ok(_) => {}
            Err(status) if status.code() == tonic::Code::Unavailable => {}
            Err(status) => panic!("SearchFlows failed: {status}"),
        }
        assert!(
            tokio::time::Instant::now() < deadline,
            "the purged run stayed searchable: {}",
            server.log()
        );
        tokio::time::sleep(Duration::from_millis(200)).await;
    }
    let acks_after_purge = metric_value(
        &scrape_metrics(metrics_address).await,
        "dexzd_search_outbox_ack_proposals_total{outcome=\"ok\"}",
    );
    let deadline = tokio::time::Instant::now() + Duration::from_secs(30);
    while metric_value(
        &scrape_metrics(metrics_address).await,
        "dexzd_search_outbox_ack_proposals_total{outcome=\"ok\"}",
    ) <= acks_after_purge
    {
        assert!(
            tokio::time::Instant::now() < deadline,
            "no ack followed the purge: {}",
            server.log()
        );
        tokio::time::sleep(Duration::from_millis(100)).await;
    }
    let status = server.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", server.log());
    assert_eq!(count_outbox_entries(&directory.path().join("store")), 0);
    primary.shutdown().await;
    schema.drop_schema().await;
}

/// Starts a pull Flow that waits in its first Step, so only Attribute writes change it.
async fn start_idle_flow(flow_client: &mut FlowServiceClient<Channel>, flow_id: &str) {
    flow_client
        .start_flow(StartFlowRequest {
            flow_id: flow_id.to_owned(),
            flow_type: FLOW_TYPE.to_owned(),
            start_step_type: "IdleStep".to_owned(),
            flow_start_options: Some(FlowStartOptions {
                flow_config_override: Some(FlowConfig {
                    worker_target: Some(WorkerTarget { is_pull_mode: true }),
                    ..FlowConfig::default()
                }),
                ..FlowStartOptions::default()
            }),
            request_id: format!("{flow_id}-start"),
            ..StartFlowRequest::default()
        })
        .await
        .expect("StartFlow succeeds");
}

async fn set_counter(flow_client: &mut FlowServiceClient<Channel>, flow_id: &str, counter: i64) {
    flow_client
        .set_attributes(SetAttributesRequest {
            flow_id: flow_id.to_owned(),
            attributes: vec![AttributeWrite {
                key: COUNTER_ATTRIBUTE.to_owned(),
                value: Some(Value {
                    kind: Some(value::Kind::IntValue(counter)),
                }),
                index_config: Some(IndexConfig {
                    enable: true,
                    r#type: IndexType::Int as i32,
                    ..IndexConfig::default()
                }),
                ..AttributeWrite::default()
            }],
            request_id: format!("{flow_id}-counter-{counter}"),
            ..SetAttributesRequest::default()
        })
        .await
        .expect("an Attribute write commits");
}

/// Returns every listed Flow of the test type, keyed by Flow ID.
async fn search_entries(
    flow_client: &mut FlowServiceClient<Channel>,
) -> Result<BTreeMap<String, SearchFlowsResponseEntry>, tonic::Status> {
    let response = flow_client
        .search_flows(SearchFlowsRequest {
            query: format!("FlowType = '{FLOW_TYPE}'"),
            page_size: 1_000,
            ..SearchFlowsRequest::default()
        })
        .await?
        .into_inner();
    assert!(
        response.next_page_token.is_empty(),
        "one page lists every Flow"
    );
    Ok(response
        .flow_runs
        .into_iter()
        .map(|entry| (entry.flow_id.clone(), entry))
        .collect())
}

/// Returns each Flow's run, status, and indexed Attributes in key order.
fn comparable_entries(
    entries: &BTreeMap<String, SearchFlowsResponseEntry>,
) -> BTreeMap<String, (String, i32, Vec<String>)> {
    entries
        .iter()
        .map(|(flow_id, entry)| {
            let mut attributes: Vec<String> = entry
                .indexed_attributes
                .iter()
                .map(|attribute| format!("{}={:?}", attribute.key, attribute.value))
                .collect();
            attributes.sort();
            (
                flow_id.clone(),
                (entry.run_id.clone(), entry.flow_status, attributes),
            )
        })
        .collect()
}

/// Returns each listed Flow's indexed counter.
async fn search_counters(
    flow_client: &mut FlowServiceClient<Channel>,
) -> Result<BTreeMap<String, i64>, tonic::Status> {
    Ok(search_entries(flow_client)
        .await?
        .into_iter()
        .filter_map(|(flow_id, entry)| {
            entry
                .indexed_attributes
                .iter()
                .find(|attribute| attribute.key == COUNTER_ATTRIBUTE)
                .and_then(|attribute| match attribute.value.as_ref()?.kind {
                    Some(value::Kind::IntValue(counter)) => Some((flow_id, counter)),
                    _ => None,
                })
        })
        .collect())
}

/// Asserts with one SearchFlows call, so read-your-writes must already hold.
async fn assert_counters_now(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_ids: &[String],
    counter: i64,
) -> BTreeMap<String, SearchFlowsResponseEntry> {
    let entries = search_entries(flow_client)
        .await
        .expect("SearchFlows sees committed writes without polling");
    for flow_id in flow_ids {
        let actual = entries.get(flow_id).and_then(|entry| {
            entry
                .indexed_attributes
                .iter()
                .find(|attribute| attribute.key == COUNTER_ATTRIBUTE)
                .and_then(|attribute| attribute.value.clone())
        });
        assert_eq!(
            actual,
            Some(Value {
                kind: Some(value::Kind::IntValue(counter)),
            }),
            "{flow_id}"
        );
    }
    entries
}

/// Polls through Unavailable answers until every Flow shows `counter`.
async fn search_until_counters(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_ids: &[String],
    counter: i64,
    server: &ServeProcess,
) {
    let deadline = tokio::time::Instant::now() + Duration::from_secs(90);
    loop {
        let last_result = match search_counters(flow_client).await {
            Ok(counters) => {
                if flow_ids
                    .iter()
                    .all(|flow_id| counters.get(flow_id) == Some(&counter))
                {
                    return;
                }
                format!("{counters:?}")
            }
            Err(status) if status.code() == tonic::Code::Unavailable => status.to_string(),
            Err(status) => panic!("SearchFlows failed: {status}"),
        };
        assert!(
            tokio::time::Instant::now() < deadline,
            "Flows did not reach counter {counter}: {last_result}; server log: {}",
            server.log()
        );
        tokio::time::sleep(Duration::from_millis(200)).await;
    }
}

async fn wait_for_metric(address: SocketAddr, sample: &str, server: &ServeProcess) {
    let deadline = tokio::time::Instant::now() + Duration::from_secs(30);
    loop {
        let body = scrape_metrics(address).await;
        if metric_value(&body, sample) > 0.0 {
            return;
        }
        assert!(
            tokio::time::Instant::now() < deadline,
            "{sample} stayed zero: {body}; server log: {}",
            server.log()
        );
        tokio::time::sleep(Duration::from_millis(200)).await;
    }
}

async fn scrape_metrics(address: SocketAddr) -> String {
    let mut stream = tokio::net::TcpStream::connect(address)
        .await
        .expect("metrics listener is reachable");
    stream
        .write_all(b"GET /metrics HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
        .await
        .expect("request is sent");
    let mut response = Vec::new();
    stream
        .read_to_end(&mut response)
        .await
        .expect("response is received");
    String::from_utf8(response).expect("metrics text is UTF-8")
}

/// Returns the value of the sample line that starts with `sample`, or zero.
fn metric_value(body: &str, sample: &str) -> f64 {
    body.lines()
        .find(|line| {
            line.strip_prefix(sample)
                .is_some_and(|rest| rest.starts_with(' '))
        })
        .and_then(|line| line.split_whitespace().last())
        .and_then(|value| value.parse().ok())
        .unwrap_or(0.0)
}

/// Counts SearchOutbox entries in a stopped process's local store.
fn count_outbox_entries(store_path: &Path) -> usize {
    let store = RocksDbLocalStore::open(store_path, RocksDbStoreOptions::default())
        .expect("the stopped process's store opens");
    (0..PARTITION_COUNT)
        .map(|partition| {
            let (start, end) =
                partition_index_range(PartitionId(partition), IndexKind::SearchOutbox);
            store
                .scan(
                    ColumnFamily::PartitionIndex,
                    Some(&start),
                    Some(&end),
                    ScanDirection::Forward,
                    usize::MAX,
                )
                .expect("the store scans")
                .len()
        })
        .sum()
}

/// Returns every partition's stored projection mode; an absent record means disabled.
fn stored_modes(store_path: &Path) -> Vec<bool> {
    let store = RocksDbLocalStore::open(store_path, RocksDbStoreOptions::default())
        .expect("the stopped process's store opens");
    (0..PARTITION_COUNT)
        .map(|partition| {
            store
                .get(
                    ColumnFamily::PartitionIndex,
                    &search_projection_mode_key(PartitionId(partition)),
                )
                .expect("the store reads")
                .map(|bytes| {
                    SearchProjectionModeRecord::decode(bytes.as_slice())
                        .expect("the mode record decodes")
                })
                .is_some_and(|mode| mode.enabled)
        })
        .collect()
}
