// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

/// An Attribute Store failure. Messages name the store, the operation, and the table.
#[derive(Debug, thiserror::Error)]
pub enum AttributeStoreError {
    /// The settings fail Go's configuration validation or a zd-only rule.
    #[error("Attribute Store {store:?} settings are invalid: {reason}")]
    InvalidSettings {
        /// The store name.
        store: String,
        /// Go's validation message.
        reason: String,
    },
    /// The DSN cannot be parsed or its TLS settings cannot be loaded.
    #[error("Attribute Store {store:?} connection settings are invalid: {reason}")]
    InvalidConnectionSettings {
        /// The store name.
        store: String,
        /// Why the DSN is unusable, with the DSN redacted.
        reason: String,
    },
    /// A store or AttributeMap table does not satisfy the table contract.
    #[error("Attribute Store {store:?} table {table} is invalid: {reason}")]
    InvalidTable {
        /// The store name.
        store: String,
        /// The quoted, schema-qualified table, or the configured name when it cannot be resolved.
        table: String,
        /// Go's validation message.
        reason: String,
    },
    /// The fencing table is missing or has the wrong shape.
    #[error(
        "Attribute Store {store:?} fencing table {table} is missing or invalid: {reason}; \
         create it as the table owner with `{ddl}` and grant SELECT, INSERT, UPDATE on it to the Dex role"
    )]
    InvalidFencingTable {
        /// The store name.
        store: String,
        /// The quoted, schema-qualified fencing table.
        table: String,
        /// What is missing or wrong.
        reason: String,
        /// The `CREATE TABLE` statement for the expected table.
        ddl: String,
    },
    /// Postgres or the connection to it failed.
    #[error("Attribute Store {store:?} failed to {operation}: {message}")]
    Database {
        /// The store name.
        store: String,
        /// The operation, naming its table when there is one.
        operation: String,
        /// The Postgres error, with the DSN redacted.
        message: String,
        /// The SQLSTATE, when Postgres reported one.
        sqlstate: Option<String>,
    },
    /// An operation exceeded its deadline; the connection was abandoned.
    #[error("Attribute Store {store:?} timed out after {timeout:?} trying to {operation}")]
    Timeout {
        /// The store name.
        store: String,
        /// The operation that timed out.
        operation: String,
        /// The deadline that elapsed.
        timeout: Duration,
    },
    /// A newer token is recorded for this cluster and scope, so the transaction rolled back.
    #[error(
        "Attribute Store {store:?} rejected a write from cluster {cluster_id:?} scope {scope:?} \
         with fencing token {token}: a newer token is recorded"
    )]
    Fenced {
        /// The store name.
        store: String,
        /// The fenced cluster ID.
        cluster_id: String,
        /// The fenced writer scope.
        scope: String,
        /// The rejected token.
        token: u64,
    },
    /// The fencing token does not fit in a Postgres bigint.
    #[error(
        "Attribute Store {store:?} cannot record fencing token {token}: it exceeds the bigint range"
    )]
    InvalidFencingToken {
        /// The store name.
        store: String,
        /// The rejected token.
        token: u64,
    },
}

impl AttributeStoreError {
    /// Returns true for database and timeout failures, which Go retries until the retry budget runs out,
    /// and false for fencing and invalid settings, tables, or tokens, which a retry cannot fix.
    pub fn is_retryable(&self) -> bool {
        matches!(self, Self::Database { .. } | Self::Timeout { .. })
    }

    /// Returns the SQLSTATE of a database failure, or None for other failures and connection errors.
    pub fn sqlstate(&self) -> Option<&str> {
        match self {
            Self::Database { sqlstate, .. } => sqlstate.as_deref(),
            _ => None,
        }
    }
}

/// Go's `redactStorageSecret`: replaces every occurrence of a non-empty secret with `[REDACTED]`.
pub(crate) fn redact_secret(message: String, secret: &str) -> String {
    if secret.is_empty() || !message.contains(secret) {
        return message;
    }
    message.replace(secret, "[REDACTED]")
}

/// Builds a database failure from a tokio-postgres error, redacting the DSN.
pub(crate) fn database_error(
    store: &str,
    dsn: &str,
    operation: impl Into<String>,
    error: &tokio_postgres::Error,
) -> AttributeStoreError {
    let (message, sqlstate) = match error.as_db_error() {
        Some(database_error) => {
            let mut message = format!(
                "SQLSTATE {}: {}",
                database_error.code().code(),
                database_error.message()
            );
            if let Some(detail) = database_error.detail() {
                message.push_str("; detail: ");
                message.push_str(detail);
            }
            if let Some(constraint) = database_error.constraint() {
                message.push_str("; constraint: ");
                message.push_str(constraint);
            }
            (message, Some(database_error.code().code().to_owned()))
        }
        None => (error.to_string(), None),
    };
    AttributeStoreError::Database {
        store: store.to_owned(),
        operation: operation.into(),
        message: redact_secret(message, dsn),
        sqlstate,
    }
}
