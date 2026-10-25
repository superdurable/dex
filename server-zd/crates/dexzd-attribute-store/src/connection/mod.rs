// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

#[cfg(test)]
mod connection_tests;

use dexzd_postgres_connection::{ConnectionError, OpenConnection, error_with_sources};
use tokio::task::JoinHandle;
use tokio_postgres::Client;

use crate::error::{AttributeStoreError, database_error};
use crate::settings::AttributeStoreSettings;

pub(crate) struct ConnectionSettings(dexzd_postgres_connection::ConnectionSettings);

/// One open connection and the task that drives it; dropping it closes the connection.
pub(crate) struct ConnectionSlot {
    pub(crate) client: Client,
    connection_task: JoinHandle<()>,
}

impl ConnectionSettings {
    pub(crate) fn parse(dsn: &str) -> Result<Self, String> {
        dexzd_postgres_connection::ConnectionSettings::parse(dsn)
            .map(Self)
            .map_err(|error| error.to_string())
    }

    /// Opens a connection bounded by the DSN's `connect_timeout` or ten seconds.
    pub(crate) async fn connect(
        &self,
        settings: &AttributeStoreSettings,
    ) -> Result<ConnectionSlot, AttributeStoreError> {
        let OpenConnection { client, driver } =
            self.0.connect().await.map_err(|error| match error {
                ConnectionError::Database(source) => database_error(
                    &settings.name,
                    &settings.dsn,
                    "connect to Postgres",
                    &source,
                ),
                ConnectionError::Timeout(timeout) => AttributeStoreError::Timeout {
                    store: settings.name.clone(),
                    operation: "connect to Postgres".to_owned(),
                    timeout,
                },
            })?;
        let store = settings.name.clone();
        let connection_task = tokio::spawn(async move {
            if let Err(error) = driver.await {
                let error = error_with_sources(&error);
                tracing::warn!(store, %error, "Attribute Store connection closed");
            }
        });
        Ok(ConnectionSlot {
            client,
            connection_task,
        })
    }
}

impl ConnectionSlot {
    pub(crate) fn is_closed(&self) -> bool {
        self.client.is_closed() || self.connection_task.is_finished()
    }
}

impl Drop for ConnectionSlot {
    fn drop(&mut self) {
        self.connection_task.abort();
    }
}
