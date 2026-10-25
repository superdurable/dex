// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Attribute Sync to PostgreSQL through the dev server; database tests run only when `DEX_ZD_PARADEDB_URL`
//! names a PostgreSQL database.

use std::collections::BTreeMap;
use std::net::SocketAddr;
use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    AttributeMatch, AttributeMatchOperator, AttributeStoreNames, AttributeSyncConfig,
    AttributeWrite, FlowConfig, FlowErrorType, FlowResetType, FlowStartOptions, FlowStatus,
    GetFlowSummaryRequest, ResetFlowRequest, SetAttributesRequest, StartFlowRequest, Value,
    WaitForAttributeRequest, WaitForFlowRequest, WorkerTarget, value,
};
use dexzd_server::DevCommandOptions;
use tokio::io::copy_bidirectional;
use tokio::sync::watch;
use tokio::task::{JoinHandle, JoinSet};
use tokio::time::{Instant, MissedTickBehavior};
use tokio_postgres::NoTls;
use tonic::transport::Channel;

use crate::harness::DevServer;

const STORE: &str = "orders";
const FLOW_ID: &str = "attribute-sync-order-1";
const ROW_WAIT: Duration = Duration::from_secs(20);

/// Concurrent Flows that each write an increasing counter during the latency measurement.
const LATENCY_FLOW_COUNT: u32 = 40;
/// SetAttributes calls of each Flow; the last counter equals this.
const LATENCY_WRITES_PER_FLOW: u32 = 150;
/// Pause between one Flow's writes; the Flows' first writes are spread across it.
const LATENCY_WRITE_INTERVAL: Duration = Duration::from_millis(250);
/// Pause between table reads; a sample is late by at most one read period.
const LATENCY_POLL_INTERVAL: Duration = Duration::from_millis(10);
/// How long after the last scheduled write every final counter must be visible.
const LATENCY_DRAIN_TIMEOUT: Duration = Duration::from_secs(30);
/// The p99 bound of commit-to-table latency.
const LATENCY_P99_BOUND: Duration = Duration::from_secs(2);

fn string_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}

/// The value that clears an Attribute; it deletes an AttributeMap instance row.
fn null_value() -> Value {
    Value {
        kind: Some(value::Kind::NullValue(0)),
    }
}

fn synced(key: &str, value: Option<Value>) -> AttributeWrite {
    AttributeWrite {
        key: key.to_owned(),
        value,
        sync_config: Some(AttributeSyncConfig { enabled: true }),
        ..AttributeWrite::default()
    }
}

/// Main row, AttributeMap instance rows, and writes through the outage proxy; then time travel.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn synced_attributes_reach_postgres_through_outages_and_time_travel() {
    let Ok(database_url) = std::env::var("DEX_ZD_PARADEDB_URL") else {
        return;
    };
    let database = AttributeStoreDatabase::create(&database_url, true).await;
    let mut proxy = PostgresTcpProxy::start(&database_url).await;
    let server = DevServer::start_with_config_overrides(Some(
        &database.store_config_yaml(&proxy.connection_string(&database_url)),
    ))
    .await;
    let mut flow_client = server.flow_client();

    let unknown_store = flow_client
        .start_flow(start_request(
            "attribute-sync-unknown",
            "missing",
            Vec::new(),
        ))
        .await
        .expect_err("an unconfigured store name is rejected");
    assert_eq!(unknown_store.code(), tonic::Code::InvalidArgument);
    assert!(
        unknown_store
            .message()
            .contains("unknown Attribute Store \"missing\""),
        "{}",
        unknown_store.message()
    );

    let started = flow_client
        .start_flow(start_request(
            FLOW_ID,
            STORE,
            vec![
                synced("Status", Some(string_value("new"))),
                synced("Items/a", Some(string_value("first"))),
                AttributeWrite {
                    key: "Owner".to_owned(),
                    value: Some(string_value("not synced")),
                    ..AttributeWrite::default()
                },
            ],
        ))
        .await
        .expect("StartFlow succeeds")
        .into_inner();
    database
        .wait_for_rows("the start attributes", Some("new"), &[("a", "first")])
        .await;

    set_attributes(
        &mut flow_client,
        "request-paid",
        vec![
            synced("Status", Some(string_value("paid"))),
            synced("Items/a", Some(null_value())),
            synced("Items/b", Some(string_value("second"))),
        ],
    )
    .await;
    database
        .wait_for_rows(
            "an instance added and another deleted",
            Some("paid"),
            &[("b", "second")],
        )
        .await;

    flow_client
        .reset_flow(ResetFlowRequest {
            flow_id: FLOW_ID.to_owned(),
            run_id: started.run_id.clone(),
            reset_type: FlowResetType::Beginning as i32,
            skip_writes_reapply: true,
            ..ResetFlowRequest::default()
        })
        .await
        .expect("ResetFlow succeeds");
    database
        .wait_for_rows(
            "the rows rewritten after time travel",
            Some("new"),
            &[("a", "first")],
        )
        .await;

    proxy.cut().await;
    set_attributes(
        &mut flow_client,
        "request-shipped",
        vec![synced("Status", Some(string_value("shipped")))],
    )
    .await;
    tokio::time::sleep(Duration::from_secs(2)).await;
    assert_eq!(
        database.status().await.as_deref(),
        Some("new"),
        "the row keeps its value while the store is unreachable"
    );
    proxy.restore();
    database
        .wait_for_rows(
            "the backlog after the store recovers",
            Some("shipped"),
            &[("a", "first")],
        )
        .await;

    server.shutdown().await;
    proxy.shutdown().await;
    database.drop_schema().await;
}

/// When WaitForFlow returns, the table holds the last SetAttributes the run accepted.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn wait_for_flow_returns_after_every_accepted_synced_write_is_in_the_table() {
    let Ok(database_url) = std::env::var("DEX_ZD_PARADEDB_URL") else {
        return;
    };
    let database = AttributeStoreDatabase::create(&database_url, true).await;
    let server =
        DevServer::start_with_config_overrides(Some(&database.store_config_yaml(&database_url)))
            .await;
    let flow_id = "attribute-sync-close-after-rows";
    let mut flow_client = server.flow_client();
    flow_client
        .start_flow(closing_start_request(
            flow_id,
            vec![synced("Status", Some(string_value(&counter_text(0))))],
        ))
        .await
        .expect("StartFlow succeeds");
    // Writes land before and after the one-second timeout; each one can hold the close.
    let writer = tokio::spawn(write_until_closed(
        server.flow_client(),
        flow_id.to_owned(),
        Instant::now() + Duration::from_secs(2),
    ));
    let result = flow_client
        .wait_for_flow(WaitForFlowRequest {
            flow_id: flow_id.to_owned(),
            wait_time_seconds: 30,
            ..WaitForFlowRequest::default()
        })
        .await
        .expect("WaitForFlow returns the close")
        .into_inner();
    let status_at_close = database.status_of(flow_id).await;
    let last_accepted = writer.await.expect("the writer completes");
    assert_eq!(result.flow_status, FlowStatus::Failed as i32);
    assert_eq!(result.error_type, FlowErrorType::FlowTimeout as i32);
    assert!(
        last_accepted > 0,
        "some writes were accepted before the close"
    );
    assert_eq!(
        status_at_close,
        Some(counter_text(last_accepted)),
        "the close waited for the last accepted write"
    );
    server.shutdown().await;
    database.drop_schema().await;
}

/// A store outage holds a natural close: the run stays RUNNING, update-class requests fail, and
/// writes are still accepted until the store recovers.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_store_outage_delays_a_natural_close_until_the_store_recovers() {
    let Ok(database_url) = std::env::var("DEX_ZD_PARADEDB_URL") else {
        return;
    };
    let database = AttributeStoreDatabase::create(&database_url, true).await;
    let mut proxy = PostgresTcpProxy::start(&database_url).await;
    let server = DevServer::start_with_config_overrides(Some(
        &database.store_config_yaml(&proxy.connection_string(&database_url)),
    ))
    .await;
    let flow_id = "attribute-sync-close-after-outage";
    let mut flow_client = server.flow_client();
    proxy.cut().await;
    flow_client
        .start_flow(closing_start_request(
            flow_id,
            vec![synced("Status", Some(string_value("started")))],
        ))
        .await
        .expect("StartFlow succeeds");
    // The one-second Flow timeout has fired; its close waits for the unreachable store.
    tokio::time::sleep(Duration::from_secs(3)).await;
    let summary = flow_client
        .get_flow_summary(GetFlowSummaryRequest {
            flow_id: flow_id.to_owned(),
            ..GetFlowSummaryRequest::default()
        })
        .await
        .expect("GetFlowSummary succeeds")
        .into_inner();
    assert_eq!(summary.flow_status, FlowStatus::Running as i32);
    let still_running = flow_client
        .wait_for_flow(WaitForFlowRequest {
            flow_id: flow_id.to_owned(),
            wait_time_seconds: 1,
            ..WaitForFlowRequest::default()
        })
        .await
        .expect_err("WaitForFlow keeps waiting during the outage");
    assert_eq!(still_running.code(), tonic::Code::DeadlineExceeded);
    let attribute_wait = flow_client
        .wait_for_attribute(WaitForAttributeRequest {
            flow_id: flow_id.to_owned(),
            r#match: Some(AttributeMatch {
                key: "Status".to_owned(),
                operator: AttributeMatchOperator::Equal as i32,
                operand: Some(string_value("never")),
            }),
            ..WaitForAttributeRequest::default()
        })
        .await
        .expect_err("WaitForAttribute fails during terminal cleanup");
    assert_eq!(attribute_wait.code(), tonic::Code::FailedPrecondition);
    assert_eq!(
        attribute_wait.message(),
        "flow terminal cleanup is in progress"
    );
    flow_client
        .set_attributes(SetAttributesRequest {
            flow_id: flow_id.to_owned(),
            attributes: vec![synced("Status", Some(string_value("written-in-cleanup")))],
            request_id: "request-in-cleanup".to_owned(),
            ..SetAttributesRequest::default()
        })
        .await
        .expect("SetAttributes is accepted during terminal cleanup");
    assert_eq!(database.status_of(flow_id).await, None);

    proxy.restore();
    let result = flow_client
        .wait_for_flow(WaitForFlowRequest {
            flow_id: flow_id.to_owned(),
            wait_time_seconds: 30,
            ..WaitForFlowRequest::default()
        })
        .await
        .expect("the close finalizes once the store recovers")
        .into_inner();
    assert_eq!(result.flow_status, FlowStatus::Failed as i32);
    assert_eq!(result.error_type, FlowErrorType::FlowTimeout as i32);
    assert_eq!(
        database.status_of(flow_id).await.as_deref(),
        Some("written-in-cleanup"),
        "the row is in the table when WaitForFlow returns"
    );
    server.shutdown().await;
    proxy.shutdown().await;
    database.drop_schema().await;
}

/// Under steady SetAttributes load from concurrent Flows, p99 commit-to-table latency stays within 2 s.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn commit_to_table_p99_latency_stays_within_two_seconds_under_steady_load() {
    let Ok(database_url) = std::env::var("DEX_ZD_PARADEDB_URL") else {
        return;
    };
    let database = AttributeStoreDatabase::create(&database_url, true).await;
    let server =
        DevServer::start_with_config_overrides(Some(&database.store_config_yaml(&database_url)))
            .await;
    let flow_ids: Vec<String> = (0..LATENCY_FLOW_COUNT)
        .map(|index| format!("attribute-sync-latency-{index:02}"))
        .collect();
    let mut flow_client = server.flow_client();
    for flow_id in &flow_ids {
        flow_client
            .start_flow(start_request(
                flow_id,
                STORE,
                vec![synced("Status", Some(string_value(&counter_text(0))))],
            ))
            .await
            .expect("StartFlow succeeds");
    }
    // The samples exclude warm-up, such as the first store connection and schema load.
    database
        .observe_counters(&flow_ids, 0, Instant::now() + ROW_WAIT)
        .await;

    let load_started = Instant::now();
    let mut writers = JoinSet::new();
    for (index, flow_id) in (0..LATENCY_FLOW_COUNT).zip(&flow_ids) {
        let first_write_at = load_started + LATENCY_WRITE_INTERVAL * index / LATENCY_FLOW_COUNT;
        let flow_client = server.flow_client();
        let flow_id = flow_id.clone();
        writers.spawn(async move {
            let committed_at = write_counter_series(flow_client, &flow_id, first_write_at).await;
            (index, committed_at)
        });
    }
    let observation_deadline =
        load_started + LATENCY_WRITE_INTERVAL * LATENCY_WRITES_PER_FLOW + LATENCY_DRAIN_TIMEOUT;
    let (observations, committed_at) = tokio::join!(
        database.observe_counters(&flow_ids, LATENCY_WRITES_PER_FLOW, observation_deadline),
        collect_commit_times(writers),
    );
    let load_duration = committed_at
        .iter()
        .filter_map(|flow_committed_at| flow_committed_at.last())
        .max()
        .expect("every Flow wrote")
        .duration_since(load_started);

    let mut latencies = commit_to_visible_latencies(&observations, &committed_at);
    latencies.sort_unstable();
    let write_count = LATENCY_FLOW_COUNT * LATENCY_WRITES_PER_FLOW;
    assert_eq!(
        latencies.len(),
        usize::try_from(write_count).expect("the write count fits usize")
    );
    let p50 = nearest_rank_percentile(&latencies, 500);
    let p90 = nearest_rank_percentile(&latencies, 900);
    let p99 = nearest_rank_percentile(&latencies, 990);
    let max = nearest_rank_percentile(&latencies, 1000);
    eprintln!(
        "attribute_sync_commit_to_table_latency: samples={} flows={LATENCY_FLOW_COUNT} writes_per_second={:.1} p50_ms={:.1} p90_ms={:.1} p99_ms={:.1} max_ms={:.1} poll_interval_ms={} longest_poll_gap_ms={:.1} (warm-up excluded; each sample is late by at most one poll gap)",
        latencies.len(),
        f64::from(write_count) / load_duration.as_secs_f64(),
        milliseconds(p50),
        milliseconds(p90),
        milliseconds(p99),
        milliseconds(max),
        LATENCY_POLL_INTERVAL.as_millis(),
        milliseconds(observations.longest_poll_gap),
    );
    assert!(
        p99 <= LATENCY_P99_BOUND,
        "p99 commit-to-table latency {p99:?} exceeds {LATENCY_P99_BOUND:?}"
    );

    server.shutdown().await;
    database.drop_schema().await;
}

/// The Status text of a latency counter; it parses back to the counter.
fn counter_text(counter: u32) -> String {
    format!("{counter:06}")
}

/// Writes counters 1 through `LATENCY_WRITES_PER_FLOW` to `flow_id`, one per interval from `first_write_at`.
///
/// Returns when each SetAttributes call returned, which is after its write committed.
async fn write_counter_series(
    mut flow_client: FlowServiceClient<Channel>,
    flow_id: &str,
    first_write_at: Instant,
) -> Vec<Instant> {
    let mut ticks = tokio::time::interval_at(first_write_at, LATENCY_WRITE_INTERVAL);
    ticks.set_missed_tick_behavior(MissedTickBehavior::Delay);
    let mut committed_at = Vec::new();
    for counter in 1..=LATENCY_WRITES_PER_FLOW {
        ticks.tick().await;
        flow_client
            .set_attributes(SetAttributesRequest {
                flow_id: flow_id.to_owned(),
                attributes: vec![synced("Status", Some(string_value(&counter_text(counter))))],
                request_id: format!("{flow_id}-counter-{counter}"),
                ..SetAttributesRequest::default()
            })
            .await
            .expect("SetAttributes succeeds");
        committed_at.push(Instant::now());
    }
    committed_at
}

/// Returns each writer's commit times at its Flow's index.
async fn collect_commit_times(mut writers: JoinSet<(u32, Vec<Instant>)>) -> Vec<Vec<Instant>> {
    let mut committed_at = vec![Vec::new(); writers.len()];
    while let Some(joined) = writers.join_next().await {
        let (index, flow_committed_at) = joined.expect("a counter writer completes");
        committed_at[usize::try_from(index).expect("a Flow index fits usize")] = flow_committed_at;
    }
    committed_at
}

/// Returns, for every write, the time from its commit until its row showed its counter or a later one.
fn commit_to_visible_latencies(
    observations: &CounterObservations,
    committed_at: &[Vec<Instant>],
) -> Vec<Duration> {
    let mut latencies = Vec::new();
    for (increases, flow_committed_at) in observations.increases.iter().zip(committed_at) {
        for (counter, committed) in (1..).zip(flow_committed_at) {
            let visible_at = increases
                .iter()
                .find(|(observed_counter, _)| *observed_counter >= counter)
                .map(|(_, observed_at)| *observed_at)
                .expect("every Flow's final counter was observed");
            // A row can appear before the client receives the SetAttributes reply.
            latencies.push(visible_at.saturating_duration_since(*committed));
        }
    }
    latencies
}

/// Returns the nearest-rank percentile of `sorted`, with `per_mille` in 1..=1000.
fn nearest_rank_percentile(sorted: &[Duration], per_mille: usize) -> Duration {
    let rank = (sorted.len() * per_mille).div_ceil(1000).max(1);
    sorted[rank - 1]
}

fn milliseconds(duration: Duration) -> f64 {
    duration.as_secs_f64() * 1000.0
}

/// Startup fails with the CREATE TABLE statement when the fencing table is missing.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn startup_fails_with_the_fencing_table_ddl_when_it_is_missing() {
    let Ok(database_url) = std::env::var("DEX_ZD_PARADEDB_URL") else {
        return;
    };
    let database = AttributeStoreDatabase::create(&database_url, false).await;
    let error = failed_dev_server_start(&database.store_config_yaml(&database_url)).await;
    assert!(error.contains("CREATE TABLE"), "{error}");
    assert!(error.contains("dexzd_attribute_sync_fencing"), "{error}");
    database.drop_schema().await;
}

/// Like the Go Server, an unreachable Attribute Store fails startup instead of serving.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn startup_fails_when_an_attribute_store_is_unreachable() {
    let unused_port = std::net::TcpListener::bind(SocketAddr::from(([127, 0, 0, 1], 0)))
        .and_then(|listener| listener.local_addr())
        .expect("a free loopback port exists")
        .port();
    let error = failed_dev_server_start(&format!(
        "attributeStore:\n  stores:\n    {STORE}:\n      type: postgres\n      dsn: \"host=127.0.0.1 port={unused_port} user=dex password=unused-secret dbname=app connect_timeout=2\"\n      tableName: orders\n"
    ))
    .await;
    assert!(error.contains("Attribute Store"), "{error}");
    assert!(
        !error.contains("unused-secret"),
        "the DSN is redacted: {error}"
    );
}

/// Starts the dev server with `yaml` as its overrides and returns the startup error.
async fn failed_dev_server_start(yaml: &str) -> String {
    let data_directory = tempfile::TempDir::new().expect("a temporary data directory");
    let config_path = data_directory.path().join("overrides.yaml");
    std::fs::write(&config_path, yaml).expect("the override file can be written");
    let listen = std::net::TcpListener::bind(SocketAddr::from(([127, 0, 0, 1], 0)))
        .and_then(|listener| listener.local_addr())
        .expect("a free loopback port exists");
    match dexzd_server::start_dev_server(&DevCommandOptions {
        data_dir: data_directory.path().to_path_buf(),
        listen,
        log_dir: None,
        config: Some(config_path),
        attribute_store_config: None,
    })
    .await
    {
        Ok(server) => {
            server.shutdown().await.expect("the server stops");
            panic!("startup succeeded with an unusable Attribute Store");
        }
        Err(error) => error.to_string(),
    }
}

fn start_request(flow_id: &str, store_name: &str, writes: Vec<AttributeWrite>) -> StartFlowRequest {
    StartFlowRequest {
        flow_id: flow_id.to_owned(),
        flow_type: "AttributeSyncFlow".to_owned(),
        start_step_type: "WaitStep".to_owned(),
        flow_start_options: Some(FlowStartOptions {
            attributes: writes,
            flow_config_override: Some(FlowConfig {
                worker_target: Some(WorkerTarget {
                    is_pull_mode: true,
                    ..WorkerTarget::default()
                }),
                attribute_store_names: Some(AttributeStoreNames {
                    names: vec![store_name.to_owned()],
                }),
                ..FlowConfig::default()
            }),
            ..FlowStartOptions::default()
        }),
        request_id: format!("request-{flow_id}"),
        ..StartFlowRequest::default()
    }
}

/// A run whose first Step never runs, so its one-second Flow timeout closes it naturally.
fn closing_start_request(flow_id: &str, writes: Vec<AttributeWrite>) -> StartFlowRequest {
    StartFlowRequest {
        flow_timeout_seconds: 1,
        ..start_request(flow_id, STORE, writes)
    }
}

/// Writes an increasing synced counter until the run rejects writes or `stop_at` passes.
///
/// Returns the last counter the run accepted.
async fn write_until_closed(
    mut flow_client: FlowServiceClient<Channel>,
    flow_id: String,
    stop_at: Instant,
) -> u32 {
    let mut last_accepted = 0;
    for counter in 1.. {
        if Instant::now() >= stop_at {
            return last_accepted;
        }
        let written = flow_client
            .set_attributes(SetAttributesRequest {
                flow_id: flow_id.clone(),
                attributes: vec![synced("Status", Some(string_value(&counter_text(counter))))],
                request_id: format!("request-{counter}"),
                ..SetAttributesRequest::default()
            })
            .await;
        match written {
            Ok(_) => last_accepted = counter,
            Err(status) if status.code() == tonic::Code::NotFound => return last_accepted,
            Err(status) => panic!("SetAttributes failed: {status}"),
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    unreachable!("the counter loop ends when the run closes")
}

async fn set_attributes(
    flow_client: &mut dexzd_protocol::dex::flow_service_client::FlowServiceClient<
        tonic::transport::Channel,
    >,
    request_id: &str,
    writes: Vec<AttributeWrite>,
) {
    flow_client
        .set_attributes(SetAttributesRequest {
            flow_id: FLOW_ID.to_owned(),
            attributes: writes,
            request_id: request_id.to_owned(),
            ..SetAttributesRequest::default()
        })
        .await
        .expect("SetAttributes succeeds");
}

/// When each Flow's counter row was first seen at each higher counter.
struct CounterObservations {
    /// Per Flow index, every (counter, observed at) increase in order.
    increases: Vec<Vec<(u32, Instant)>>,
    /// Longest time between two table reads, which bounds how late a sample can be.
    longest_poll_gap: Duration,
}

/// A schema with the store tables and, optionally, the fencing table.
struct AttributeStoreDatabase {
    client: tokio_postgres::Client,
    connection: JoinHandle<()>,
    schema: String,
}

impl AttributeStoreDatabase {
    async fn create(database_url: &str, has_fencing_table: bool) -> Self {
        let (client, connection) = tokio_postgres::connect(database_url, NoTls)
            .await
            .expect("the test connects to PostgreSQL");
        let connection = tokio::spawn(async move {
            connection
                .await
                .expect("the PostgreSQL test connection stays open");
        });
        let nonce = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .expect("the wall clock is after the Unix epoch")
            .as_nanos();
        let schema = format!("dexzd_attribute_sync_{}_{nonce}", std::process::id());
        let mut statements = format!(
            "CREATE SCHEMA {schema};
             CREATE TABLE {schema}.orders (flow_id text PRIMARY KEY, status text);
             CREATE TABLE {schema}.order_items (flow_id text NOT NULL, item_key text NOT NULL,
               value text, PRIMARY KEY (flow_id, item_key));"
        );
        if has_fencing_table {
            statements.push_str(&format!(
                "CREATE TABLE {schema}.dexzd_attribute_sync_fencing (store_name text NOT NULL,
                   cluster_id text NOT NULL, scope text NOT NULL, fencing_token bigint NOT NULL,
                   updated_at timestamptz NOT NULL, PRIMARY KEY (store_name, cluster_id, scope));"
            ));
        }
        client
            .batch_execute(&statements)
            .await
            .expect("the test tables are created");
        Self {
            client,
            connection,
            schema,
        }
    }

    fn store_config_yaml(&self, connection_string: &str) -> String {
        let schema = &self.schema;
        format!(
            "attributeStore:\n  stores:\n    {STORE}:\n      type: postgres\n      dsn: \"{connection_string}\"\n      tableName: {schema}.orders\n      columnNaming: snake_case\n      attributeMaps:\n        Items:\n          tableName: {schema}.order_items\n          instanceKeyColumn: item_key\n      fencingTableName: {schema}.dexzd_attribute_sync_fencing\n  syncRetryPolicy:\n    maximumInterval: 500ms\n    totalDuration: 168h\n"
        )
    }

    async fn status(&self) -> Option<String> {
        self.status_of(FLOW_ID).await
    }

    async fn status_of(&self, flow_id: &str) -> Option<String> {
        self.client
            .query_opt(
                &format!(
                    "SELECT status FROM {}.orders WHERE flow_id = $1",
                    self.schema
                ),
                &[&flow_id],
            )
            .await
            .expect("the main row can be read")
            .and_then(|row| row.get::<_, Option<String>>(0))
    }

    async fn items(&self) -> Vec<(String, String)> {
        self.client
            .query(
                &format!(
                    "SELECT item_key, value FROM {}.order_items WHERE flow_id = $1 ORDER BY item_key",
                    self.schema
                ),
                &[&FLOW_ID],
            )
            .await
            .expect("the instance rows can be read")
            .into_iter()
            .map(|row| (row.get(0), row.get(1)))
            .collect()
    }

    /// Polls until the main row's status and the instance rows match.
    async fn wait_for_rows(&self, description: &str, status: Option<&str>, items: &[(&str, &str)]) {
        let expected_items: Vec<(String, String)> = items
            .iter()
            .map(|(key, value)| ((*key).to_owned(), (*value).to_owned()))
            .collect();
        let deadline = tokio::time::Instant::now() + ROW_WAIT;
        loop {
            let actual_status = self.status().await;
            let actual_items = self.items().await;
            if actual_status.as_deref() == status && actual_items == expected_items {
                return;
            }
            assert!(
                tokio::time::Instant::now() < deadline,
                "timed out waiting for {description}: status {actual_status:?}, items {actual_items:?}"
            );
            tokio::time::sleep(Duration::from_millis(100)).await;
        }
    }

    /// Polls the main table until every Flow of `flow_ids` shows `final_counter`.
    ///
    /// Returns when each row's counter first rose. Panics when a counter moves backwards or the
    /// rows are not final by `deadline`.
    async fn observe_counters(
        &self,
        flow_ids: &[String],
        final_counter: u32,
        deadline: Instant,
    ) -> CounterObservations {
        let flow_indexes: BTreeMap<&str, usize> = flow_ids
            .iter()
            .enumerate()
            .map(|(index, flow_id)| (flow_id.as_str(), index))
            .collect();
        let query = format!("SELECT flow_id, status FROM {}.orders", self.schema);
        let mut latest_counters: Vec<Option<u32>> = vec![None; flow_ids.len()];
        let mut observations = CounterObservations {
            increases: vec![Vec::new(); flow_ids.len()],
            longest_poll_gap: Duration::ZERO,
        };
        let mut previous_poll_at: Option<Instant> = None;
        loop {
            let rows = self
                .client
                .query(&query, &[])
                .await
                .expect("the main rows can be read");
            // Stamped after the read returns, so each sample is an upper bound.
            let observed_at = Instant::now();
            if let Some(previous_poll_at) = previous_poll_at.replace(observed_at) {
                observations.longest_poll_gap = observations
                    .longest_poll_gap
                    .max(observed_at.duration_since(previous_poll_at));
            }
            for row in rows {
                let flow_id: String = row.get(0);
                let status: Option<String> = row.get(1);
                let Some(&index) = flow_indexes.get(flow_id.as_str()) else {
                    continue;
                };
                let counter: u32 = status
                    .as_deref()
                    .and_then(|text| text.parse().ok())
                    .unwrap_or_else(|| panic!("the row of {flow_id} holds a counter: {status:?}"));
                if let Some(previous_counter) = latest_counters[index] {
                    assert!(
                        counter >= previous_counter,
                        "the row of {flow_id} moved back from {previous_counter} to {counter}"
                    );
                    if counter == previous_counter {
                        continue;
                    }
                }
                latest_counters[index] = Some(counter);
                observations.increases[index].push((counter, observed_at));
            }
            if latest_counters
                .iter()
                .all(|counter| *counter == Some(final_counter))
            {
                return observations;
            }
            assert!(
                Instant::now() < deadline,
                "timed out waiting for every row to show counter {final_counter}: {latest_counters:?}"
            );
            tokio::time::sleep(LATENCY_POLL_INTERVAL).await;
        }
    }

    async fn drop_schema(self) {
        self.client
            .batch_execute(&format!("DROP SCHEMA {} CASCADE", self.schema))
            .await
            .expect("the test schema is dropped");
        self.connection.abort();
    }
}

/// Relays PostgreSQL connections and can cut them while the server keeps committing.
struct PostgresTcpProxy {
    address: SocketAddr,
    availability: watch::Sender<bool>,
    active_connections: watch::Receiver<usize>,
    task: JoinHandle<()>,
}

impl PostgresTcpProxy {
    async fn start(database_url: &str) -> Self {
        let configuration = database_url
            .parse::<tokio_postgres::Config>()
            .expect("the PostgreSQL URL is valid");
        let host = match configuration.get_hosts().first() {
            Some(tokio_postgres::config::Host::Tcp(host)) => host.clone(),
            _ => panic!("the PostgreSQL URL names a TCP host"),
        };
        let port = configuration.get_ports().first().copied().unwrap_or(5432);
        let listener = tokio::net::TcpListener::bind(SocketAddr::from(([127, 0, 0, 1], 0)))
            .await
            .expect("the proxy binds a loopback port");
        let address = listener.local_addr().expect("the proxy has an address");
        let (availability, mut is_available) = watch::channel(true);
        let (connection_count, active_connections) = watch::channel(0_usize);
        let task = tokio::spawn(async move {
            let mut connections = JoinSet::new();
            loop {
                tokio::select! {
                    accepted = listener.accept() => {
                        let (incoming, _) = accepted.expect("the proxy accepts connections");
                        if !*is_available.borrow() {
                            continue;
                        }
                        connection_count.send_modify(|count| *count += 1);
                        let active = connection_count.clone();
                        let availability = is_available.clone();
                        let host = host.clone();
                        connections.spawn(async move {
                            relay_connection(incoming, &host, port, availability).await;
                            active.send_modify(|count| *count -= 1);
                        });
                    }
                    Some(result) = connections.join_next(), if !connections.is_empty() => {
                        result.expect("a proxy connection task completes");
                    }
                    changed = is_available.changed() => {
                        changed.expect("the proxy controller stays open");
                    }
                }
            }
        });
        Self {
            address,
            availability,
            active_connections,
            task,
        }
    }

    fn connection_string(&self, database_url: &str) -> String {
        let (credentials, authority_and_path) = database_url
            .rsplit_once('@')
            .expect("the PostgreSQL URL includes credentials");
        let (_, path) = authority_and_path
            .split_once('/')
            .expect("the PostgreSQL URL includes a database path");
        format!("{credentials}@127.0.0.1:{}/{path}", self.address.port())
    }

    async fn cut(&mut self) {
        self.availability
            .send(false)
            .expect("the proxy task remains live");
        while *self.active_connections.borrow() != 0 {
            self.active_connections
                .changed()
                .await
                .expect("the proxy reports active connections");
        }
    }

    fn restore(&self) {
        self.availability
            .send(true)
            .expect("the proxy task remains live");
    }

    async fn shutdown(mut self) {
        self.task.abort();
        let error = (&mut self.task)
            .await
            .expect_err("the proxy task is aborted");
        assert!(error.is_cancelled(), "the proxy task failed: {error}");
    }
}

impl Drop for PostgresTcpProxy {
    fn drop(&mut self) {
        self.task.abort();
    }
}

async fn relay_connection(
    mut incoming: tokio::net::TcpStream,
    host: &str,
    port: u16,
    mut availability: watch::Receiver<bool>,
) {
    // Closing the incoming connection reports an unreachable database to the client.
    let Ok(mut outgoing) = tokio::net::TcpStream::connect((host, port)).await else {
        return;
    };
    tokio::select! {
        // Resets are expected when either side closes the relay.
        _relayed = copy_bidirectional(&mut incoming, &mut outgoing) => {}
        changed = availability.changed() => {
            changed.expect("the proxy controller stays open");
        }
    }
}
