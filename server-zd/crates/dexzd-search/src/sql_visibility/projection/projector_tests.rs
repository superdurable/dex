// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_keyspace::{PartitionId, RunId, TenantId};
use dexzd_postgres_connection::{ConnectionSettings, error_with_sources};
use dexzd_protocol::dex::{EncodedObject, FlowStatus, IndexType, Value, value};
use dexzd_protocol::dexzd::v1::{LogPosition, SearchAttributeValue, SearchDocumentRecord};
use prost::Message;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;
use tokio_postgres::{Config, NoTls};

use super::{
    ProjectionError, database_error, row_from_document, should_retry, upsert_database_error,
    validate_position,
};

#[test]
fn permits_zero_incarnation_with_positive_sequence() {
    let position = LogPosition {
        node_id: "node".into(),
        incarnation: 0,
        seq: 1,
    };
    assert!(validate_position(&position).is_ok());
}

#[test]
fn projects_closed_documents_without_close_time() {
    let tenant_id = TenantId(1);
    let run_id = RunId([8; 16]);
    let record = SearchDocumentRecord {
        version: 1,
        tenant_id: tenant_id.0,
        flow_id: "closed-flow".into(),
        run_id: run_id.as_bytes().to_vec(),
        flow_type: "ClosedFlow".into(),
        status: FlowStatus::Completed.into(),
        started_at_micros: 1_000_000,
        closed_at_micros: 0,
        ..SearchDocumentRecord::default()
    };
    let row = row_from_document(PartitionId(3), tenant_id, run_id, &record.encode_to_vec())
        .expect("closed document without close time is supported");
    assert_eq!(row.execution_status, "Completed");
    assert_eq!(row.close_time_ns, None);
}

#[test]
fn projects_work_queue_permissions_into_their_column_only() {
    let tenant_id = TenantId(1);
    let run_id = RunId([9; 16]);
    let record = SearchDocumentRecord {
        version: 2,
        tenant_id: tenant_id.0,
        flow_id: "permission-flow".into(),
        run_id: run_id.as_bytes().to_vec(),
        flow_type: "PermissionFlow".into(),
        status: FlowStatus::Running.into(),
        started_at_micros: 1_000_000,
        work_queue_permissions: vec!["approve".into()],
        attributes: vec![
            SearchAttributeValue {
                index_key: "DexWorkQueuePermissions".into(),
                index_type: IndexType::KeywordArray.into(),
                value: Some(Value {
                    kind: Some(value::Kind::ObjValue(EncodedObject {
                        encoding: "json".into(),
                        payload: br#"["approve"]"#.to_vec(),
                    })),
                }),
            },
            SearchAttributeValue {
                index_key: "ReviewState".into(),
                index_type: IndexType::Keyword.into(),
                value: Some(Value {
                    kind: Some(value::Kind::StringValue("ready".into())),
                }),
            },
        ],
        ..SearchDocumentRecord::default()
    };
    let row = row_from_document(PartitionId(3), tenant_id, run_id, &record.encode_to_vec())
        .expect("a document with server-managed permissions projects");
    assert_eq!(row.dex_work_queue_permissions, Some(vec!["approve".into()]));
    assert!(!row.sa_keyword.contains_key("DexWorkQueuePermissions"));
    assert!(row.sa_keyword.contains_key("ReviewState"));
    assert!(row.sa_wire.contains_key("DexWorkQueuePermissions"));
}

#[tokio::test]
async fn database_errors_keep_neither_credentials_nor_the_raw_postgres_error() {
    let dsn = "postgres://dex:p%40ss-word@db.example.com/dex?sslmode=disable";
    let settings = ConnectionSettings::parse_for_search(dsn).expect("valid URL DSN");
    let fields = |sqlstate: &str| {
        format!(
            "SFATAL\0VFATAL\0C{sqlstate}\0Mrejected {dsn}\0Dpassword p@ss-word or p%40ss-word expired\0\0"
        )
    };
    let partition_id = PartitionId(3);
    let first_run_id = RunId([7; 16]);
    let last_run_id = RunId([8; 16]);
    let rows = [first_run_id, last_run_id].map(|run_id| {
        let record = SearchDocumentRecord {
            version: 1,
            tenant_id: 1,
            flow_id: "error-flow".into(),
            run_id: run_id.as_bytes().to_vec(),
            flow_type: "ErrorFlow".into(),
            status: FlowStatus::Running.into(),
            started_at_micros: 1_000_000,
            ..SearchDocumentRecord::default()
        };
        row_from_document(partition_id, TenantId(1), run_id, &record.encode_to_vec()).unwrap()
    });
    let errors = [
        (
            database_error(
                "read progress from",
                rejected_startup_error(fields("08006")).await,
            ),
            true,
        ),
        (
            upsert_database_error(
                partition_id,
                &[&rows[0]],
                rejected_startup_error(fields("28P01")).await,
            ),
            false,
        ),
        (
            upsert_database_error(
                partition_id,
                &[&rows[0], &rows[1]],
                rejected_startup_error(fields("08006")).await,
            ),
            true,
        ),
        (
            upsert_database_error(
                partition_id,
                &[&rows[0], &rows[1]],
                rejected_startup_error(fields("28P01")).await,
            ),
            false,
        ),
    ];
    assert!(
        matches!(&errors[1].0, ProjectionError::RowDatabase { partition_id: partition, run_id, .. }
        if *partition == partition_id && *run_id == first_run_id)
    );
    assert!(
        matches!(&errors[2].0, ProjectionError::BatchDatabase { partition_id: partition, row_count: 2, first_run_id: first, last_run_id: last, .. }
        if *partition == partition_id && *first == first_run_id && *last == last_run_id)
    );
    for (error, is_retryable) in errors {
        let error = error.redact_credentials(&settings);
        assert_eq!(error.is_retryable(), is_retryable, "{error}");
        assert!(std::error::Error::source(&error).is_none(), "{error:?}");
        for text in [
            error.to_string(),
            format!("{error:?}"),
            error_with_sources(&error),
        ] {
            assert!(text.contains("SQLSTATE"), "{text}");
            assert!(text.contains("[REDACTED]"), "{text}");
            for secret in [dsn, "p@ss-word", "p%40ss-word"] {
                assert!(!text.contains(secret), "{text}");
            }
        }
        let result: Result<(), ProjectionError> = Err(error);
        assert_eq!(should_retry(&result, false), is_retryable);
        assert!(!should_retry(&result, true));
    }
}

/// Returns the client error after a fake server rejects its startup with `fields`.
async fn rejected_startup_error(fields: String) -> tokio_postgres::Error {
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
        let mut message = vec![b'E'];
        message.extend_from_slice(&((fields.len() + 4) as u32).to_be_bytes());
        message.extend_from_slice(fields.as_bytes());
        stream
            .write_all(&message)
            .await
            .expect("server rejects startup");
    });
    let mut configuration = Config::new();
    configuration.host("127.0.0.1").port(port).user("test");
    let error = match configuration.connect(NoTls).await {
        Ok(_) => panic!("the rejected startup cannot connect"),
        Err(error) => error,
    };
    server.await.expect("fake server sends its rejection");
    error
}
