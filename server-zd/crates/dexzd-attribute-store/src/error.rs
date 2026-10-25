// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

use dexzd_postgres_connection::error_with_sources;
pub(crate) use dexzd_postgres_connection::redact_secret;

use crate::connection::ConnectionSettings;

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
        /// The Postgres error, with the DSN and password redacted.
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
    /// A write transaction outlasted the transaction timeout after the connection opened; the connection was abandoned.
    #[error(
        "Attribute Store {store:?} transaction timed out after {timeout:?} trying to {operation}"
    )]
    TransactionTimeout {
        /// The store name.
        store: String,
        /// The operation that timed out.
        operation: String,
        /// The transaction timeout that elapsed.
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

/// Which pending writes a failure holds back while it is retried.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AttributeStoreFailureScope {
    /// The store cannot serve any batch, such as a lost connection, a connect timeout, or failed authentication.
    Store,
    /// Postgres rejected this batch's content, such as a constraint violation, or its transaction timed out; other batches can succeed.
    Batch,
}

/// SQLSTATE classes caused by the values or columns a batch writes, so retrying other batches can succeed.
const BATCH_SQLSTATE_CLASSES: [&str; 7] = [
    "21", // cardinality violation
    "22", // data exception, such as a value too long or invalid text for the column type
    "23", // integrity constraint violation
    "42", // syntax error or access rule violation, such as an undefined column
    "44", // WITH CHECK OPTION violation
    "54", // program limit exceeded, such as an index row that is too large
    "P0", // PL/pgSQL error raised by a trigger
];

/// Raised under class 42 for a missing grant, which fails every batch of the store.
const INSUFFICIENT_PRIVILEGE_SQLSTATE: &str = "42501";

/// SQLSTATEs of a transaction that ran out of time or conflicted with another session over its rows.
const TRANSACTION_SQLSTATES: [&str; 5] = [
    "25P03", // idle_in_transaction_session_timeout ended the session
    "40001", // serialization failure
    "40P01", // deadlock detected
    "55P03", // lock not available, such as lock_timeout
    "57014", // statement_timeout, or a cancel request for this one backend
];

/// SQLSTATEs raised when the transaction ran out of time; a smaller batch may fit.
const TRANSACTION_TIMEOUT_SQLSTATES: [&str; 2] = ["25P03", "57014"];

impl AttributeStoreError {
    /// Returns true for database and timeout failures, which Go retries until the retry budget runs out,
    /// and false for fencing and invalid settings, tables, or tokens, which a retry cannot fix.
    pub fn is_retryable(&self) -> bool {
        matches!(
            self,
            Self::Database { .. } | Self::Timeout { .. } | Self::TransactionTimeout { .. }
        )
    }

    /// Returns [`AttributeStoreFailureScope::Batch`] for a transaction timeout, and for a database
    /// failure whose SQLSTATE blames the batch's values or columns, its time, or a conflict over its
    /// rows. Returns [`AttributeStoreFailureScope::Store`] for every other failure: connection
    /// failures and connect timeouts, server-side unavailability, and non-retryable failures.
    pub fn failure_scope(&self) -> AttributeStoreFailureScope {
        if matches!(self, Self::TransactionTimeout { .. }) {
            return AttributeStoreFailureScope::Batch;
        }
        let Some(sqlstate) = self.sqlstate() else {
            return AttributeStoreFailureScope::Store;
        };
        let is_batch_class = BATCH_SQLSTATE_CLASSES
            .iter()
            .any(|class| sqlstate.starts_with(class));
        if (is_batch_class && sqlstate != INSUFFICIENT_PRIVILEGE_SQLSTATE)
            || TRANSACTION_SQLSTATES.contains(&sqlstate)
        {
            AttributeStoreFailureScope::Batch
        } else {
            AttributeStoreFailureScope::Store
        }
    }

    /// Returns true when a write transaction ran out of time: a [`Self::TransactionTimeout`], or
    /// SQLSTATE 57014 or 25P03. 57014 also reports a cancel request for this backend, which is
    /// retried the same way. Returns false for every other failure, including connect timeouts.
    pub fn is_transaction_timeout(&self) -> bool {
        matches!(self, Self::TransactionTimeout { .. })
            || self
                .sqlstate()
                .is_some_and(|sqlstate| TRANSACTION_TIMEOUT_SQLSTATES.contains(&sqlstate))
    }

    /// Returns the SQLSTATE of a database failure, or None for other failures and connection errors.
    pub fn sqlstate(&self) -> Option<&str> {
        match self {
            Self::Database { sqlstate, .. } => sqlstate.as_deref(),
            _ => None,
        }
    }
}

/// Builds a database failure from a tokio-postgres error, redacting the DSN and password.
pub(crate) fn database_error(
    store: &str,
    connection_settings: &ConnectionSettings,
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
        None => (error_with_sources(error), None),
    };
    AttributeStoreError::Database {
        store: store.to_owned(),
        operation: operation.into(),
        message: connection_settings.redact_credentials(message),
        sqlstate,
    }
}

#[cfg(test)]
mod tests {
    use super::redact_secret;
    use dexzd_postgres_connection::error_with_sources;

    #[derive(Debug, thiserror::Error)]
    #[error("TLS handshake failed")]
    struct HandshakeError {
        #[source]
        source: CertificateError,
    }

    #[derive(Debug, thiserror::Error)]
    #[error("certificate rejected")]
    struct CertificateError {
        #[source]
        source: std::io::Error,
    }

    #[test]
    fn source_chain_includes_cause_and_redacts_the_whole_message() {
        let error = HandshakeError {
            source: CertificateError {
                source: std::io::Error::other("secret"),
            },
        };
        let message = redact_secret(error_with_sources(&error), "secret");
        assert_eq!(
            message,
            "TLS handshake failed: certificate rejected: [REDACTED]"
        );
    }
}
