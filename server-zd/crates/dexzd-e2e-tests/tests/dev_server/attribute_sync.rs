// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Attribute Sync to PostgreSQL through the dev server; database tests run only when `DEX_ZD_PARADEDB_URL`
//! names a PostgreSQL database.

use std::net::SocketAddr;
use std::time::Duration;

use dexzd_protocol::dex::{
    AttributeStoreNames, AttributeSyncConfig, AttributeWrite, FlowConfig, FlowResetType,
    FlowStartOptions, ResetFlowRequest, SetAttributesRequest, StartFlowRequest, Value,
    WorkerTarget, value,
};
use dexzd_server::DevCommandOptions;
use tokio::io::copy_bidirectional;
use tokio::sync::watch;
use tokio::task::{JoinHandle, JoinSet};
use tokio_postgres::NoTls;

use crate::harness::DevServer;

const STORE: &str = "orders";
const FLOW_ID: &str = "attribute-sync-order-1";
const ROW_WAIT: Duration = Duration::from_secs(20);

fn string_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
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
        vec![
            synced("Status", Some(string_value("paid"))),
            synced("Items/a", None),
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

async fn set_attributes(
    flow_client: &mut dexzd_protocol::dex::flow_service_client::FlowServiceClient<
        tonic::transport::Channel,
    >,
    writes: Vec<AttributeWrite>,
) {
    flow_client
        .set_attributes(SetAttributesRequest {
            flow_id: FLOW_ID.to_owned(),
            attributes: writes,
            ..SetAttributesRequest::default()
        })
        .await
        .expect("SetAttributes succeeds");
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
            "attributeStore:\n  stores:\n    {STORE}:\n      type: postgres\n      dsn: \"{connection_string}\"\n      tableName: {schema}.orders\n      columnNaming: snake_case\n      attributeMaps:\n        Items:\n          tableName: {schema}.order_items\n          instanceKeyColumn: item_key\n      fencingTableName: {schema}.dexzd_attribute_sync_fencing\n  syncRetryPolicy:\n    maximumInterval: 500ms\n"
        )
    }

    async fn status(&self) -> Option<String> {
        self.client
            .query_opt(
                &format!(
                    "SELECT status FROM {}.orders WHERE flow_id = $1",
                    self.schema
                ),
                &[&FLOW_ID],
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
