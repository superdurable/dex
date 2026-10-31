// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use thiserror::Error;
use tokio_postgres::Client;
use tokio_postgres::Transaction;
use tokio_postgres::types::{ToSql, Type};

use super::VisibilityDialect;

const SCHEMA_ADVISORY_LOCK: i64 = 5_589_034_107_693_961_330;

/// A failure while creating or checking the SQL visibility schema.
#[derive(Debug, Error)]
#[non_exhaustive]
pub enum SchemaError {
    /// The schema belongs to another visibility backend.
    #[error(
        "visibility schema dialect {found} does not match configured backend {expected}; use a new database or schema"
    )]
    DialectMismatch {
        /// Dialect recorded by the schema.
        found: String,
        /// Dialect requested by the server.
        expected: &'static str,
    },
    /// PostgreSQL is older than the minimum supported version.
    #[error("PostgreSQL visibility requires PostgreSQL 15 or newer; server version is {found}")]
    UnsupportedPostgresVersion {
        /// Numeric PostgreSQL server version.
        found: i32,
    },
    /// The database has a schema newer than this binary supports.
    #[error("SQL visibility schema version {found} exceeds supported version {supported}")]
    NewerVersion {
        /// Version stored in the SQL visibility schema.
        found: i32,
        /// Highest version this binary supports.
        supported: i32,
    },
    /// A schema operation failed in Postgres.
    #[error("{operation} SQL visibility schema: {source}")]
    Database {
        /// Failed schema operation.
        operation: &'static str,
        /// Postgres failure.
        #[source]
        source: tokio_postgres::Error,
    },
}

/// Installs or upgrades the visibility schema atomically and rejects newer versions.
pub async fn ensure_schema(client: &mut Client) -> Result<(), SchemaError> {
    ensure_schema_for_dialect(client, VisibilityDialect::ParadeDb).await
}

/// Installs the requested dialect, preserving version-three ParadeDB documents in place.
pub async fn ensure_schema_for_dialect(
    client: &mut Client,
    dialect: VisibilityDialect,
) -> Result<(), SchemaError> {
    let transaction = client
        .transaction()
        .await
        .map_err(|source| SchemaError::Database {
            operation: "begin transaction for",
            source,
        })?;
    transaction
        .batch_execute("SET LOCAL lock_timeout = '5s'")
        .await
        .map_err(|source| SchemaError::Database {
            operation: "set lock timeout for",
            source,
        })?;
    let previous_version = schema_version(&transaction).await?;
    if previous_version.is_some() {
        check_dialect(&transaction, dialect).await?;
    }
    if is_current_version(previous_version, dialect)? && has_recorded_dialect(&transaction).await? {
        return commit_schema(transaction).await;
    }
    transaction
        .query_typed_one(
            "SELECT pg_advisory_xact_lock($1::bigint)",
            &[(&SCHEMA_ADVISORY_LOCK as &(dyn ToSql + Sync), Type::INT8)],
        )
        .await
        .map_err(|source| SchemaError::Database {
            operation: "lock migration of",
            source,
        })?;
    let previous_version = schema_version(&transaction).await?;
    if previous_version.is_some() {
        check_dialect(&transaction, dialect).await?;
    }
    if is_current_version(previous_version, dialect)? && has_recorded_dialect(&transaction).await? {
        return commit_schema(transaction).await;
    }
    if dialect == VisibilityDialect::Postgres {
        let row = transaction
            .query_typed_one("SELECT current_setting('server_version_num')::integer", &[])
            .await
            .map_err(|source| SchemaError::Database {
                operation: "read PostgreSQL version for",
                source,
            })?;
        let found = row.get(0);
        if found < 150_000 {
            return Err(SchemaError::UnsupportedPostgresVersion { found });
        }
    }
    transaction
        .batch_execute("CREATE TABLE IF NOT EXISTS dexzd_visibility_schema_version (id integer PRIMARY KEY CHECK (id = 1), version integer NOT NULL)")
        .await
        .map_err(|source| SchemaError::Database {
            operation: "create version table for",
            source,
        })?;
    transaction.batch_execute("ALTER TABLE dexzd_visibility_schema_version ADD COLUMN IF NOT EXISTS dialect text NOT NULL DEFAULT 'paradedb'").await.map_err(|source| SchemaError::Database { operation: "add dialect to", source })?;
    if previous_version == Some(3) && dialect == VisibilityDialect::ParadeDb {
        return commit_schema(transaction).await;
    }
    transaction
        .batch_execute(include_str!("migration.sql"))
        .await
        .map_err(|source| SchemaError::Database {
            operation: "migrate",
            source,
        })?;
    transaction
        .batch_execute(match dialect {
            VisibilityDialect::ParadeDb => include_str!("paradedb.sql"),
            VisibilityDialect::Postgres => include_str!("postgres.sql"),
        })
        .await
        .map_err(|source| SchemaError::Database {
            operation: "migrate dialect of",
            source,
        })?;
    if previous_version.is_none_or(|version| version < 2) {
        transaction
            .batch_execute("TRUNCATE TABLE dexzd_visibility_runs, dexzd_visibility_progress, dexzd_visibility_skipped_documents")
            .await
            .map_err(|source| SchemaError::Database {
                operation: "clear older projection in",
                source,
            })?;
    }
    let supported_version = supported_version(dialect);
    transaction
        .execute_typed(
            "INSERT INTO dexzd_visibility_schema_version (id, version, dialect) VALUES (1, $1::integer, $2::text) ON CONFLICT (id) DO UPDATE SET version = EXCLUDED.version, dialect = EXCLUDED.dialect",
            &[(&supported_version as &(dyn ToSql + Sync), Type::INT4), (&dialect.name(), Type::TEXT)],
        )
        .await
        .map_err(|source| SchemaError::Database {
            operation: "record version of",
            source,
        })?;
    commit_schema(transaction).await
}

async fn schema_version(transaction: &Transaction<'_>) -> Result<Option<i32>, SchemaError> {
    let table_row = transaction
        .query_typed_one(
            "SELECT to_regclass(format('%I.dexzd_visibility_schema_version', current_schema())) IS NOT NULL",
            &[],
        )
        .await
        .map_err(|source| SchemaError::Database {
            operation: "find version table for",
            source,
        })?;
    if !table_row.get::<_, bool>(0) {
        return Ok(None);
    }
    transaction
        .query_typed_opt(
            "SELECT version FROM dexzd_visibility_schema_version WHERE id = 1",
            &[],
        )
        .await
        .map(|row| row.map(|row| row.get(0)))
        .map_err(|source| SchemaError::Database {
            operation: "read version of",
            source,
        })
}

fn is_current_version(
    version: Option<i32>,
    dialect: VisibilityDialect,
) -> Result<bool, SchemaError> {
    let supported = supported_version(dialect);
    if let Some(found) = version {
        if found > supported {
            return Err(SchemaError::NewerVersion { found, supported });
        }
        return Ok(found == supported);
    }
    Ok(false)
}

async fn commit_schema(transaction: Transaction<'_>) -> Result<(), SchemaError> {
    transaction
        .commit()
        .await
        .map_err(|source| SchemaError::Database {
            operation: "commit",
            source,
        })
}

async fn check_dialect(
    transaction: &Transaction<'_>,
    dialect: VisibilityDialect,
) -> Result<(), SchemaError> {
    let row = transaction.query_typed_opt("SELECT coalesce(to_jsonb(version_row)->>'dialect', 'paradedb') FROM dexzd_visibility_schema_version AS version_row WHERE id = 1", &[]).await.map_err(|source| SchemaError::Database { operation: "read dialect of", source })?;
    if let Some(row) = row {
        let found: String = row.get(0);
        if found != dialect.name() {
            return Err(SchemaError::DialectMismatch {
                found,
                expected: dialect.name(),
            });
        }
    }
    Ok(())
}

fn supported_version(dialect: VisibilityDialect) -> i32 {
    match dialect {
        VisibilityDialect::ParadeDb => 3,
        VisibilityDialect::Postgres => 4,
    }
}

async fn has_recorded_dialect(transaction: &Transaction<'_>) -> Result<bool, SchemaError> {
    let row = transaction.query_typed_opt("SELECT to_jsonb(version_row)->>'dialect' IS NOT NULL FROM dexzd_visibility_schema_version AS version_row WHERE id = 1", &[]).await.map_err(|source| SchemaError::Database { operation: "inspect dialect metadata in", source })?;
    Ok(row.is_some_and(|row| row.get(0)))
}
