// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Replays `testdata/pre-rpc-publication-rejection-log`, which server-zd recorded before apply
//! rejected an RPC publication during a pending close. Its RPC and TimeTravel commands carry no
//! rejection rule, so recovery must reach the recorded state.

use dexzd_protocol::dexzd::v1::flow_command::Command;

use crate::format_decoding::decode_objects;
use crate::golden_replay::{SnapshotUse, assert_replays_to_recorded_state};
use crate::testdata_tree::{OBJECTS_DIRECTORY, TestdataVersion, read_files, testdata_root};

/// A log that server-zd at `1d37cc7f5` recorded; see its README.
const OLDER_LOG_DIRECTORY: &str = "pre-rpc-publication-rejection-log";

#[tokio::test(start_paused = true)]
async fn older_rpc_publications_during_a_pending_close_recover_to_the_recorded_state() {
    let version = TestdataVersion {
        name: OLDER_LOG_DIRECTORY.to_owned(),
        directory: testdata_root().join(OLDER_LOG_DIRECTORY),
    };
    let decoded = decode_objects(&read_files(&version.directory.join(OBJECTS_DIRECTORY)));
    let older_publishing_rpcs = decoded
        .commands
        .iter()
        .filter(|command| {
            matches!(
                &command.command,
                Some(Command::RpcCommitted(rpc))
                    if !rpc.should_reject_publication_during_pending_close
                        && rpc
                            .response
                            .as_ref()
                            .is_some_and(|response| !response.publish_to_channel.is_empty())
            )
        })
        .count();
    let older_time_travels = decoded
        .commands
        .iter()
        .filter(|command| {
            matches!(
                &command.command,
                Some(Command::TimeTravel(time_travel))
                    if !time_travel.should_reject_publication_during_pending_close
            )
        })
        .count();
    assert_eq!(
        older_publishing_rpcs, 2,
        "the log holds both older RPC publications"
    );
    assert_eq!(older_time_travels, 1, "the log holds the older TimeTravel");
    for snapshot_use in [SnapshotUse::Restore, SnapshotUse::Ignore] {
        assert_replays_to_recorded_state(&version, snapshot_use).await;
    }
}
