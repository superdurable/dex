// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::VecDeque;
use std::ops::{Deref, DerefMut};
use std::sync::Arc;
use std::sync::Mutex as StandardMutex;
use std::time::Duration;

use async_trait::async_trait;
use dexzd_keyspace::{RunId, TenantId};
use dexzd_postgres_connection::{
    ConnectionError, ConnectionSettings, OpenConnection, error_with_sources,
};
use dexzd_visibility_query::parse_and_check;
use tokio::sync::{Mutex, MutexGuard, Semaphore, SemaphorePermit};
use tokio::task::JoinHandle;
use tokio_postgres::{Client, Config, Row};

use super::{
    PageCursor, SchemaError, SqlStatement, SqlTranslationError, ensure_schema, tenant_column_value,
    translate_query,
};
use crate::page_token::PagePosition;
use crate::search_backend::{DEFAULT_PAGE_SIZE, MAX_PAGE_SIZE};
use crate::{SearchBackend, SearchError, SearchPage, SearchRequest};

use super::search_result::response_entry;

pub(super) const DATABASE_OPERATION_TIMEOUT: Duration = Duration::from_secs(15);
const SCHEMA_SETUP_TIMEOUT: Duration = Duration::from_secs(60);

/// Waits for an application-defined projection watermark before reading search results.
#[async_trait]
pub trait ProjectionVisibilityWait: Send + Sync {
    /// Waits until the required writes for this request are visible.
    async fn wait_until_visible(&self, request: SearchRequest<'_>) -> Result<(), SearchError>;
}

/// Searches tenant runs through a bounded pool of PgBouncer-compatible connections.
pub struct ParadeDbSearchBackend {
    configuration: ConnectionSettings,
    slots: Vec<Mutex<ConnectionSlot>>,
    free_slots: StandardMutex<VecDeque<usize>>,
    available_slots: Semaphore,
    visibility_wait: Option<Arc<dyn ProjectionVisibilityWait>>,
    visibility_wait_timeout: Duration,
}

impl ParadeDbSearchBackend {
    /// Opens `pool_size` connections, ensures the schema, and bounds projection visibility waits.
    pub async fn connect(
        configuration: &Config,
        pool_size: usize,
        visibility_wait: Option<Arc<dyn ProjectionVisibilityWait>>,
        visibility_wait_timeout: Duration,
    ) -> Result<Self, SearchError> {
        Self::connect_with_settings(
            &ConnectionSettings::from_config(configuration.clone()),
            pool_size,
            visibility_wait,
            visibility_wait_timeout,
        )
        .await
    }

    /// Opens connections using parsed PostgreSQL TLS settings.
    pub async fn connect_with_settings(
        configuration: &ConnectionSettings,
        pool_size: usize,
        visibility_wait: Option<Arc<dyn ProjectionVisibilityWait>>,
        visibility_wait_timeout: Duration,
    ) -> Result<Self, SearchError> {
        if pool_size == 0 || visibility_wait_timeout.is_zero() {
            return Err(SearchError::Internal(
                "ParadeDB search pool size and visibility wait timeout must be positive".into(),
            ));
        }
        let mut slots = Vec::with_capacity(pool_size);
        for slot_index in 0..pool_size {
            let mut slot = ConnectionSlot::open(configuration).await?;
            if slot_index == 0 {
                slot.ensure_schema_with_timeout(configuration, SCHEMA_SETUP_TIMEOUT)
                    .await?;
            }
            slots.push(Mutex::new(slot));
        }
        Ok(Self {
            configuration: configuration.clone(),
            slots,
            free_slots: StandardMutex::new((0..pool_size).collect()),
            available_slots: Semaphore::new(pool_size),
            visibility_wait,
            visibility_wait_timeout,
        })
    }

    async fn query(&self, statement: &SqlStatement) -> Result<Vec<Row>, SearchError> {
        let mut slot = self.lock_next_slot().await;
        if slot.is_disconnected() {
            slot.reconnect(&self.configuration).await?;
        }
        // Keep query_typed unnamed: custom plans let PostgreSQL use indexes for each request's predicates.
        for attempt in 0..2 {
            match tokio::time::timeout(
                DATABASE_OPERATION_TIMEOUT,
                slot.client
                    .query_typed(&statement.sql, &statement.typed_parameter_refs()),
            )
            .await
            {
                Ok(Ok(rows)) => return Ok(rows),
                Ok(Err(error)) if is_transient_database_error(&error) && attempt == 0 => {
                    slot.reconnect(&self.configuration).await?;
                }
                Ok(Err(error)) => {
                    return Err(classify_database_error(
                        &self.configuration,
                        "query ParadeDB visibility runs",
                        error,
                    ));
                }
                Err(_) => {
                    slot.invalidate();
                    if attempt == 0 {
                        slot.reconnect(&self.configuration).await?;
                    } else {
                        return Err(SearchError::Unavailable(format!(
                            "query ParadeDB visibility runs timed out after {DATABASE_OPERATION_TIMEOUT:?}"
                        )));
                    }
                }
            }
        }
        unreachable!("the query returns on its second attempt")
    }

    pub(super) async fn lock_next_slot(&self) -> PooledConnectionSlot<'_> {
        let permit = self
            .available_slots
            .acquire()
            .await
            .expect("the owned connection pool never closes its semaphore");
        let slot_index = self
            .free_slots
            .lock()
            .expect("the connection pool slot queue cannot be poisoned")
            .pop_front()
            .expect("a semaphore permit guarantees one free connection slot");
        let slot = self.slots[slot_index]
            .try_lock()
            .expect("a free slot is unlocked before its permit is released");
        PooledConnectionSlot {
            slot,
            slot_index,
            backend: self,
            _permit: permit,
        }
    }

    pub(super) fn configuration(&self) -> &ConnectionSettings {
        &self.configuration
    }
}

pub(super) struct PooledConnectionSlot<'a> {
    slot: MutexGuard<'a, ConnectionSlot>,
    slot_index: usize,
    backend: &'a ParadeDbSearchBackend,
    _permit: SemaphorePermit<'a>,
}

impl Deref for PooledConnectionSlot<'_> {
    type Target = ConnectionSlot;

    fn deref(&self) -> &Self::Target {
        &self.slot
    }
}

impl DerefMut for PooledConnectionSlot<'_> {
    fn deref_mut(&mut self) -> &mut Self::Target {
        &mut self.slot
    }
}

impl Drop for PooledConnectionSlot<'_> {
    fn drop(&mut self) {
        self.backend
            .free_slots
            .lock()
            .expect("the connection pool slot queue cannot be poisoned")
            .push_back(self.slot_index);
    }
}

pub(super) struct ConnectionSlot {
    pub(super) client: Client,
    connection_task: JoinHandle<()>,
    is_invalidated: bool,
}

impl ConnectionSlot {
    pub(super) async fn open(configuration: &ConnectionSettings) -> Result<Self, SearchError> {
        let OpenConnection { client, driver } =
            configuration.connect().await.map_err(|error| match error {
                ConnectionError::Timeout(timeout) => SearchError::Unavailable(format!(
                    "connect to ParadeDB timed out after {timeout:?}"
                )),
                ConnectionError::Database(source) => {
                    classify_database_error(configuration, "connect to ParadeDB", source)
                }
            })?;
        let connection_settings = configuration.clone();
        let connection_task = tokio::spawn(async move {
            if let Err(error) = driver.await {
                let error = connection_settings.redact_credentials(error_with_sources(&error));
                tracing::error!(%error, "ParadeDB search connection closed");
            }
        });
        Ok(Self {
            client,
            connection_task,
            is_invalidated: false,
        })
    }

    async fn ensure_schema_with_timeout(
        &mut self,
        configuration: &ConnectionSettings,
        timeout: Duration,
    ) -> Result<(), SearchError> {
        match tokio::time::timeout(timeout, ensure_schema(&mut self.client)).await {
            Ok(result) => result.map_err(|error| schema_search_error(configuration, error)),
            Err(_) => {
                self.invalidate();
                Err(SearchError::Unavailable(format!(
                    "ensure ParadeDB visibility schema timed out after {timeout:?}"
                )))
            }
        }
    }

    pub(super) async fn reconnect(
        &mut self,
        configuration: &ConnectionSettings,
    ) -> Result<(), SearchError> {
        let replacement = Self::open(configuration).await?;
        *self = replacement;
        Ok(())
    }

    pub(super) fn is_disconnected(&self) -> bool {
        self.is_invalidated || self.client.is_closed() || self.connection_task.is_finished()
    }

    pub(super) fn invalidate(&mut self) {
        self.is_invalidated = true;
        self.connection_task.abort();
    }
}

impl Drop for ConnectionSlot {
    fn drop(&mut self) {
        self.connection_task.abort();
    }
}

#[async_trait]
impl SearchBackend for ParadeDbSearchBackend {
    async fn search(&self, request: SearchRequest<'_>) -> Result<SearchPage, SearchError> {
        let page_size = if request.page_size == 0 {
            DEFAULT_PAGE_SIZE
        } else {
            request.page_size.min(MAX_PAGE_SIZE)
        };
        let query = parse_and_check(request.query, request.field_registry)
            .map_err(|error| SearchError::InvalidArgument(error.to_string()))?;
        let position = request
            .page_token
            .map(|token| PagePosition::decode(token, request.tenant_id, request.query))
            .transpose()?;
        let cursor = position.as_ref().map(|position| PageCursor {
            is_running: position.is_running,
            close_time_ns: position.close_time_ns,
            start_time_ns: position.start_time_ns,
            run_id: position.run_id.to_string(),
        });
        let limit = (page_size + 1) as i64;
        let statement = translate_query(
            &query,
            &tenant_column_value(request.tenant_id),
            limit,
            cursor.as_ref(),
        )
        .map_err(translation_error)?;
        if let Some(visibility_wait) = &self.visibility_wait {
            tokio::time::timeout(
                self.visibility_wait_timeout,
                visibility_wait.wait_until_visible(request),
            )
            .await
            .map_err(|_| {
                SearchError::Unavailable(format!(
                    "wait for ParadeDB projection visibility timed out after {:?}",
                    self.visibility_wait_timeout
                ))
            })??;
        }
        let mut rows = self.query(&statement).await?;
        let has_next_page = rows.len() > page_size;
        if has_next_page {
            rows.truncate(page_size);
        }
        let next_page_token = if has_next_page {
            rows.last()
                .map(|row| row_position(row, request.tenant_id, request.query))
                .transpose()?
        } else {
            None
        };
        let entries = rows.iter().map(response_entry).collect::<Result<_, _>>()?;
        Ok(SearchPage {
            entries,
            next_page_token,
        })
    }
}

fn row_position(
    row: &tokio_postgres::Row,
    tenant_id: TenantId,
    query: &str,
) -> Result<String, SearchError> {
    let run_id: String = row
        .try_get("run_id")
        .map_err(|error| SearchError::Internal(format!("read ParadeDB run ID: {error}")))?;
    let run_id = run_id
        .parse::<RunId>()
        .map_err(|error| SearchError::Internal(format!("parse ParadeDB run ID: {error}")))?;
    let execution_status: String = row.try_get("execution_status").map_err(|error| {
        SearchError::Internal(format!("read ParadeDB execution status: {error}"))
    })?;
    let close_time_ns: Option<i64> = row
        .try_get("close_time_ns")
        .map_err(|error| SearchError::Internal(format!("read ParadeDB close time: {error}")))?;
    let start_time_ns: i64 = row
        .try_get("start_time_ns")
        .map_err(|error| SearchError::Internal(format!("read ParadeDB start time: {error}")))?;
    Ok(PagePosition {
        is_running: execution_status == "Running",
        close_time_ns,
        start_time_ns,
        run_id,
    }
    .encode(tenant_id, query))
}

fn translation_error(error: SqlTranslationError) -> SearchError {
    SearchError::InvalidArgument(error.to_string())
}

pub(super) fn is_transient_database_error(error: &tokio_postgres::Error) -> bool {
    error
        .as_db_error()
        .is_none_or(|database_error| is_transient_sqlstate(database_error.code().code()))
}

fn is_transient_sqlstate(sqlstate: &str) -> bool {
    sqlstate.starts_with("08")
        || sqlstate.starts_with("53")
        || matches!(
            sqlstate,
            "55P03" | "57014" | "57P01" | "57P02" | "57P03" | "57P04" | "57P05" | "40001" | "40P01"
        )
}

/// Redacts the DSN and password from the message, because server-supplied text can repeat them.
pub(super) fn classify_database_error(
    configuration: &ConnectionSettings,
    operation: &str,
    error: tokio_postgres::Error,
) -> SearchError {
    let message = configuration
        .redact_credentials(format!("{operation}: {}", database_error_message(&error)));
    if is_transient_database_error(&error) {
        SearchError::Unavailable(message)
    } else {
        SearchError::Internal(message)
    }
}

/// Formats the error without redaction; callers apply [`ConnectionSettings::redact_credentials`].
pub(super) fn database_error_message(error: &tokio_postgres::Error) -> String {
    let Some(database_error) = error.as_db_error() else {
        return format!("SQLSTATE none: {}", error_with_sources(error));
    };
    let mut message = format!(
        "SQLSTATE {}: {}",
        database_error.code().code(),
        database_error.message()
    );
    if let Some(detail) = database_error.detail() {
        message.push_str(&format!("; detail: {detail}"));
    }
    if let Some(constraint) = database_error.constraint() {
        message.push_str(&format!("; constraint: {constraint}"));
    }
    message
}

fn schema_search_error(configuration: &ConnectionSettings, error: SchemaError) -> SearchError {
    match error {
        SchemaError::Database { operation, source } => {
            classify_database_error(configuration, operation, source)
        }
        SchemaError::NewerVersion { .. } => SearchError::Internal(error.to_string()),
    }
}

#[cfg(test)]
mod tests {
    use std::future::pending;
    use std::io::Write as _;
    use std::sync::Arc;
    use std::time::Duration;

    use dexzd_postgres_connection::ConnectionSettings;
    use rcgen::generate_simple_self_signed;
    use rustls::pki_types::{PrivateKeyDer, PrivatePkcs8KeyDer};
    use tempfile::NamedTempFile;
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    use tokio::net::TcpListener;
    use tokio_postgres::{Config, NoTls};
    use tokio_rustls::TlsAcceptor;

    use super::{ConnectionSlot, classify_database_error, is_transient_database_error};
    use crate::SearchError;

    #[tokio::test]
    async fn classifies_transient_and_permanent_sqlstates() {
        for (sqlstate, is_transient) in [
            ("08006", true),
            ("08P01", true),
            ("53100", true),
            ("55P03", true),
            ("57014", true),
            ("57P01", true),
            ("57P02", true),
            ("57P03", true),
            ("57P04", true),
            ("57P05", true),
            ("40001", true),
            ("40P01", true),
            ("57P06", false),
            ("40002", false),
            ("40P02", false),
            ("23505", false),
            ("54023", false),
        ] {
            let error = rejected_startup_error(sqlstate).await;
            assert_eq!(
                error
                    .as_db_error()
                    .map(|database_error| database_error.code().code()),
                Some(sqlstate)
            );
            assert_eq!(
                is_transient_database_error(&error),
                is_transient,
                "{sqlstate}"
            );
            let classified = classify_database_error(
                &ConnectionSettings::from_config(Config::new()),
                "connect to ParadeDB",
                error,
            );
            match (is_transient, classified) {
                (true, SearchError::Unavailable(message))
                | (false, SearchError::Internal(message)) => {
                    assert!(message.contains(&format!("SQLSTATE {sqlstate}")));
                }
                (_, classified) => panic!("SQLSTATE {sqlstate} classified as {classified:?}"),
            }
        }
    }

    #[tokio::test]
    async fn connect_error_without_sqlstate_is_unavailable() {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("fake PostgreSQL listener binds");
        let port = listener
            .local_addr()
            .expect("listener has an address")
            .port();
        let server = tokio::spawn(async move {
            let (mut stream, _) = listener.accept().await.expect("client connects");
            let startup_length = stream.read_u32().await.expect("startup has a length");
            let mut startup = vec![0; startup_length as usize - 4];
            stream
                .read_exact(&mut startup)
                .await
                .expect("client sends startup parameters");
        });
        let mut configuration = Config::new();
        configuration.host("127.0.0.1").port(port).user("test");
        let error = match configuration.connect(NoTls).await {
            Ok(_) => panic!("the closed startup connection cannot connect"),
            Err(error) => error,
        };
        server.await.expect("fake server closes the connection");
        assert!(error.as_db_error().is_none());
        assert!(is_transient_database_error(&error));
        assert!(matches!(
            classify_database_error(
                &ConnectionSettings::from_config(Config::new()),
                "connect to ParadeDB",
                error
            ),
            SearchError::Unavailable(message) if message.contains("SQLSTATE none")
        ));
    }

    #[tokio::test]
    async fn tls_certificate_failure_includes_rustls_cause_without_dsn() {
        let server_certificate = generate_simple_self_signed(vec!["localhost".to_owned()])
            .expect("generate server certificate");
        let unrelated_ca = generate_simple_self_signed(vec!["unrelated.example".to_owned()])
            .expect("generate unrelated CA");
        let mut root_file = NamedTempFile::new().expect("create root certificate file");
        root_file
            .write_all(unrelated_ca.cert.pem().as_bytes())
            .expect("write unrelated root certificate");
        let server_config = rustls::ServerConfig::builder_with_provider(Arc::new(
            rustls::crypto::aws_lc_rs::default_provider(),
        ))
        .with_safe_default_protocol_versions()
        .expect("server protocol versions")
        .with_no_client_auth()
        .with_single_cert(
            vec![server_certificate.cert.der().clone()],
            PrivateKeyDer::Pkcs8(PrivatePkcs8KeyDer::from(
                server_certificate.signing_key.serialize_der(),
            )),
        )
        .expect("server certificate config");
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("fake TLS listener binds");
        let port = listener.local_addr().expect("listener address").port();
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.expect("client connects");
            let handshake = TlsAcceptor::from(Arc::new(server_config))
                .accept(stream)
                .await;
            assert!(handshake.is_err(), "client rejects untrusted certificate");
        });
        let dsn = format!(
            "host=localhost hostaddr=127.0.0.1 port={port} user=dex password=hidden-password sslmode=verify-full sslnegotiation=direct sslrootcert={} connect_timeout=5",
            root_file.path().display()
        );
        let settings = ConnectionSettings::parse_for_search(&dsn).expect("valid TLS DSN");
        let error = match ConnectionSlot::open(&settings).await {
            Ok(_) => panic!("untrusted certificate cannot connect"),
            Err(error) => error,
        };
        let message = error.to_string();
        assert!(
            message.contains("certificate") || message.contains("UnknownIssuer"),
            "{message}"
        );
        assert!(!message.contains(&dsn), "{message}");
        assert!(!message.contains("hidden-password"), "{message}");
        server.await.expect("fake server observes TLS rejection");
    }

    #[tokio::test]
    async fn schema_setup_timeout_retires_the_connection() {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("fake PostgreSQL listener binds");
        let port = listener
            .local_addr()
            .expect("listener has an address")
            .port();
        let server = tokio::spawn(async move {
            let (mut stream, _) = listener.accept().await.expect("client connects");
            let startup_length = stream.read_u32().await.expect("startup has a length");
            let mut startup = vec![0; startup_length as usize - 4];
            stream
                .read_exact(&mut startup)
                .await
                .expect("client sends startup parameters");
            stream
                .write_all(&[b'R', 0, 0, 0, 8, 0, 0, 0, 0, b'Z', 0, 0, 0, 5, b'I'])
                .await
                .expect("server accepts startup");
            assert_eq!(stream.read_u8().await.expect("schema query starts"), b'Q');
            let query_length = stream.read_u32().await.expect("query has a length");
            let mut query = vec![0; query_length as usize - 4];
            stream
                .read_exact(&mut query)
                .await
                .expect("client sends schema query");
            let mut next_byte = [0];
            assert_eq!(stream.read(&mut next_byte).await.expect("socket closes"), 0);
        });
        let mut configuration = Config::new();
        configuration.host("127.0.0.1").port(port).user("test");
        let connection_settings = ConnectionSettings::from_config(configuration);
        let mut slot = ConnectionSlot::open(&connection_settings)
            .await
            .expect("fake server accepts startup");
        let result = tokio::time::timeout(
            Duration::from_secs(1),
            slot.ensure_schema_with_timeout(&connection_settings, Duration::from_millis(50)),
        )
        .await
        .expect("schema setup has a client-side deadline");
        assert!(matches!(
            result,
            Err(SearchError::Unavailable(message)) if message.contains("schema") && message.contains("timed out")
        ));
        assert!(slot.is_disconnected());
        tokio::time::timeout(Duration::from_secs(1), server)
            .await
            .expect("stalled connection closes promptly")
            .expect("stalled connection is retired");
    }

    #[tokio::test]
    async fn connect_error_redacts_the_dsn_and_password_from_server_text() {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("fake PostgreSQL listener binds");
        let port = listener
            .local_addr()
            .expect("listener has an address")
            .port();
        let dsn = format!(
            "host=127.0.0.1 port={port} user=dex password=hidden-password connect_timeout=5"
        );
        let server = tokio::spawn(reject_startup(
            listener,
            format!(
                "SFATAL\0VFATAL\0C28P01\0Mrejected {dsn}\0Dpassword hidden-password expired\0\0"
            ),
        ));
        let settings = ConnectionSettings::parse_for_search(&dsn).expect("valid plaintext DSN");
        let error = match ConnectionSlot::open(&settings).await {
            Ok(_) => panic!("the rejected startup cannot connect"),
            Err(error) => error,
        };
        server.await.expect("fake server sends its rejection");
        let message = error.to_string();
        assert!(message.contains("SQLSTATE 28P01"), "{message}");
        assert!(message.contains("[REDACTED]"), "{message}");
        assert!(!message.contains(&dsn), "{message}");
        assert!(!message.contains("hidden-password"), "{message}");
    }

    async fn rejected_startup_error(sqlstate: &str) -> tokio_postgres::Error {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("fake PostgreSQL listener binds");
        let port = listener
            .local_addr()
            .expect("listener has an address")
            .port();
        let response = format!("SFATAL\0VFATAL\0C{sqlstate}\0Mforced rejection\0\0");
        let server = tokio::spawn(reject_startup(listener, response));
        let mut configuration = Config::new();
        configuration.host("127.0.0.1").port(port).user("test");
        let error = match configuration.connect(NoTls).await {
            Ok(_) => panic!("the rejected startup cannot connect"),
            Err(error) => error,
        };
        server.await.expect("fake server sends its rejection");
        error
    }

    /// Answers one client's startup message with the ErrorResponse fields in `response`.
    async fn reject_startup(listener: TcpListener, response: String) {
        let (mut stream, _) = listener.accept().await.expect("client connects");
        let startup_length = stream.read_u32().await.expect("startup has a length");
        let mut startup = vec![0; startup_length as usize - 4];
        stream
            .read_exact(&mut startup)
            .await
            .expect("client sends startup parameters");
        let mut message = vec![b'E'];
        message.extend_from_slice(&((response.len() + 4) as u32).to_be_bytes());
        message.extend_from_slice(response.as_bytes());
        stream
            .write_all(&message)
            .await
            .expect("server rejects startup");
    }

    #[tokio::test]
    async fn reconnects_after_a_stalled_postgres_handshake() {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("fake PostgreSQL listener binds");
        let port = listener
            .local_addr()
            .expect("listener has an address")
            .port();
        let server = tokio::spawn(async move {
            let mut connections = Vec::new();
            for connection_number in 0..3 {
                let (mut stream, _) = listener.accept().await.expect("client connects");
                let startup_length = stream.read_u32().await.expect("startup has a length");
                let mut startup = vec![0; startup_length as usize - 4];
                stream
                    .read_exact(&mut startup)
                    .await
                    .expect("client sends startup parameters");
                if connection_number != 1 {
                    stream
                        .write_all(&[b'R', 0, 0, 0, 8, 0, 0, 0, 0, b'Z', 0, 0, 0, 5, b'I'])
                        .await
                        .expect("server accepts startup");
                }
                connections.push(stream);
            }
            pending::<()>().await;
        });
        let mut configuration = tokio_postgres::Config::new();
        configuration
            .host("127.0.0.1")
            .port(port)
            .user("test")
            .dbname("test")
            .connect_timeout(Duration::from_secs(1));

        let connection_settings = ConnectionSettings::from_config(configuration);
        let mut slot = ConnectionSlot::open(&connection_settings)
            .await
            .expect("initial connection opens");
        slot.invalidate();
        assert!(slot.reconnect(&connection_settings).await.is_err());
        assert!(slot.is_disconnected());
        slot.reconnect(&connection_settings)
            .await
            .expect("a fresh connection replaces the failed slot");
        assert!(!slot.is_disconnected());
        server.abort();
        let error = server.await.expect_err("fake server is aborted");
        assert!(error.is_cancelled());
    }
}
