// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

use dexzd_postgres_connection::{ConnectionSettings, OpenConnection};
use tokio::sync::{Mutex, MutexGuard};
use tokio::task::JoinHandle;
use tokio_postgres::Client;

use crate::{ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult};

pub(crate) struct Db9Pool {
    settings: ConnectionSettings,
    slots: Vec<Mutex<Option<Db9Session>>>,
    next: AtomicUsize,
    timeout: Duration,
}

pub(crate) struct Db9Session {
    pub(crate) client: Client,
    driver: JoinHandle<()>,
}

impl Db9Pool {
    pub(crate) fn new(settings: ConnectionSettings, size: usize, timeout: Duration) -> Self {
        Self {
            settings,
            slots: (0..size).map(|_| Mutex::new(None)).collect(),
            next: AtomicUsize::new(0),
            timeout,
        }
    }

    pub(crate) async fn acquire(
        &self,
        operation: ObjectStoreOperation,
        key: &str,
    ) -> ObjectStoreResult<MutexGuard<'_, Option<Db9Session>>> {
        let start = self.next.fetch_add(1, Ordering::Relaxed) % self.slots.len();
        let mut available = None;
        for offset in 0..self.slots.len() {
            if let Ok(slot) = self.slots[(start + offset) % self.slots.len()].try_lock() {
                available = Some(slot);
                break;
            }
        }
        let mut slot = match available {
            Some(slot) => slot,
            None => tokio::time::timeout(self.timeout, self.slots[start].lock())
                .await
                .map_err(|_| {
                    ObjectStoreError::new(
                        operation,
                        key,
                        ObjectStoreErrorKind::Transient,
                        "db9 pool checkout timed out",
                    )
                })?,
        };
        if slot
            .as_ref()
            .is_none_or(|session| session.client.is_closed() || session.driver.is_finished())
        {
            *slot = None;
            let OpenConnection { client, driver } =
                self.settings.connect().await.map_err(|_| {
                    ObjectStoreError::new(
                        operation,
                        key,
                        ObjectStoreErrorKind::Transient,
                        "db9 connection failed",
                    )
                })?;
            let task = tokio::spawn(async move {
                if let Err(error) = driver.await {
                    tracing::warn!(code = ?error.code(), "db9 connection closed");
                }
            });
            *slot = Some(Db9Session {
                client,
                driver: task,
            });
        }
        Ok(slot)
    }
}

impl Drop for Db9Session {
    fn drop(&mut self) {
        self.driver.abort();
    }
}
