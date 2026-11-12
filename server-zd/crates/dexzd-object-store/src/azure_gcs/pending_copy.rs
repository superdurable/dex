// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

use object_store::path::Path as ObjectPath;
use object_store::{ObjectMeta, ObjectStore, ObjectStoreExt};
use tokio::time::Instant;

use super::error_mapping::classified_status;

const FIRST_POLL_INTERVAL: Duration = Duration::from_millis(250);
const MAX_POLL_INTERVAL: Duration = Duration::from_secs(5);

/// How an Azure copy that the service left pending ended.
#[derive(Debug)]
pub(super) enum PendingCopyEnd {
    /// The copy finished; the metadata describes the target.
    Succeeded(ObjectMeta),
    /// The copy failed or was aborted, with this `x-ms-copy-status`.
    Failed(String),
    /// The copy did not finish before the deadline, or its status could not be read.
    StillPending(String),
}

/// Polls the target of a pending Azure copy until the copy ends or `timeout` passes.
pub(super) async fn wait_for_pending_copy(
    reads: &dyn ObjectStore,
    target: &ObjectPath,
    timeout: Duration,
) -> PendingCopyEnd {
    let deadline = Instant::now() + timeout;
    let mut interval = FIRST_POLL_INTERVAL;
    let mut last_observation = "copy status was never read".to_owned();
    loop {
        if Instant::now() + interval > deadline {
            return PendingCopyEnd::StillPending(last_observation);
        }
        tokio::time::sleep(interval).await;
        interval = (interval * 2).min(MAX_POLL_INTERVAL);
        // The deadline bounds each poll too, including the read client's own retries.
        let Ok(polled) = tokio::time::timeout_at(deadline, reads.head(target)).await else {
            return PendingCopyEnd::StillPending(format!(
                "{last_observation}; the last poll timed out"
            ));
        };
        match polled {
            Ok(metadata) => return PendingCopyEnd::Succeeded(metadata),
            Err(error) => match classified_status(&error) {
                Some(status) if status.is_failed_copy() => {
                    return PendingCopyEnd::Failed(status.copy_status);
                }
                _ => last_observation = error.to_string(),
            },
        }
    }
}
