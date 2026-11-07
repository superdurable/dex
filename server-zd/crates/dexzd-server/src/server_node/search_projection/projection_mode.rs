// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_apply::FlowStateReader;
use dexzd_keyspace::row_keys::search_projection_mode_key;
use dexzd_keyspace::{ColumnFamily, PartitionId};
use dexzd_metrics::NodeMetrics;
use dexzd_protocol::dexzd::v1::{
    SearchProjectionModeRecord, SetSearchProjectionModeCommand, flow_command,
};
use prost::Message;

use crate::server_node::partition_mode::{CommittedPartitionMode, PartitionModeRebuilder};

/// Whether apply appends SearchOutbox entries, enabled while an external search backend projects them.
pub(crate) struct SearchProjectionMode;

impl CommittedPartitionMode for SearchProjectionMode {
    const NAME: &'static str = "search projection mode";

    fn is_stored_enabled(
        view: &dyn FlowStateReader,
        partition: PartitionId,
    ) -> Result<bool, String> {
        Ok(view
            .get(
                ColumnFamily::PartitionIndex,
                &search_projection_mode_key(partition),
            )
            .map_err(|error| error.to_string())?
            .map(|bytes| SearchProjectionModeRecord::decode(bytes.as_slice()))
            .transpose()
            .map_err(|error| format!("decode the search projection mode: {error}"))?
            .is_some_and(|mode| mode.enabled))
    }

    fn set_mode_command(_partition: PartitionId, is_enabled: bool) -> flow_command::Command {
        flow_command::Command::SetSearchProjectionMode(SetSearchProjectionModeCommand {
            enabled: is_enabled,
        })
    }

    fn record_skipped_partition(metrics: &NodeMetrics) {
        metrics.search_projection_mode_skipped_partitions.inc();
    }
}

/// Commits the search projection mode before partitions rebuild work, so no speculative command sees it change.
pub(crate) type SearchProjectionModeRebuilder = PartitionModeRebuilder<SearchProjectionMode>;
