// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_protocol::dexzd::v1::LogPosition;
use dexzd_search::sql_visibility::ProjectionProgress;

use super::{InitialLoadReason, PartitionHead, initial_load_reason};

fn head() -> PartitionHead {
    PartitionHead {
        applied_position: Some(LogPosition {
            node_id: "node".into(),
            incarnation: 1,
            seq: 9,
        }),
        last_seq: 500,
        is_enabled: true,
        acked_through_seq: 300,
        enabled_at_seq: 100,
    }
}

fn progress(outbox_through_seq: Option<u64>) -> ProjectionProgress {
    ProjectionProgress {
        applied_position: Some(LogPosition {
            node_id: "node".into(),
            incarnation: 1,
            seq: 7,
        }),
        applied_epoch: Some(2),
        outbox_through_seq,
    }
}

#[test]
fn progress_continues_only_between_the_acked_seq_and_the_views_last_seq() {
    let head = head();
    for through_seq in [300, 400, 500] {
        assert_eq!(
            initial_load_reason(Some(&progress(Some(through_seq))), &head),
            None,
            "{through_seq}"
        );
    }
    let cases = [
        (None, InitialLoadReason::NoProgress),
        (
            Some(ProjectionProgress {
                applied_position: None,
                ..progress(Some(400))
            }),
            InitialLoadReason::NoProgress,
        ),
        (Some(progress(None)), InitialLoadReason::NoOutboxProgress),
        (
            Some(progress(Some(99))),
            InitialLoadReason::EnabledAfterProgress,
        ),
        (
            Some(progress(Some(299))),
            InitialLoadReason::AckedAfterProgress,
        ),
        (
            Some(progress(Some(501))),
            InitialLoadReason::AheadOfCommittedState,
        ),
    ];
    for (recorded, reason) in cases {
        assert_eq!(
            initial_load_reason(recorded.as_ref(), &head),
            Some(reason),
            "{recorded:?}"
        );
    }
}
