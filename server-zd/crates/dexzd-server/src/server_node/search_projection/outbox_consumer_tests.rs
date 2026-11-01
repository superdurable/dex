// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;

use dexzd_keyspace::PartitionId;
use dexzd_protocol::dexzd::v1::LogPosition;
use dexzd_search::sql_visibility::{
    PartitionProjectionBatch, ProjectionProgress, ProjectionVisibilityWait,
};
use dexzd_search::{SearchError, SearchRequest};

use super::super::outbox_acks::AckSegment;
use super::super::outbox_watermarks::SearchOutboxWatermarks;
use super::{
    InitialLoadReason, PartitionHead, PartitionProjection, UnexpectedStopRecorder,
    initial_load_reason,
};

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

fn ack_segment(through_seq: u64, entry_count: u64) -> AckSegment {
    AckSegment {
        through_seq,
        entry_count,
    }
}

fn outbox_batch(is_initial_load: bool, outbox_through_seq: u64) -> PartitionProjectionBatch {
    PartitionProjectionBatch {
        partition_id: PartitionId(0),
        is_initial_load,
        changes: Vec::new(),
        outbox_through_seq,
        applied_position: None,
    }
}

/// SQL progress is at 400 while an ack through 400 is in flight, and two passes fail.
#[test]
fn acks_never_cover_seqs_that_sql_progress_does_not_record() {
    let mut state = PartitionProjection {
        recorded: Some(progress(Some(400))),
        ..PartitionProjection::default()
    };
    state.check_recorded_progress(&head());
    assert!(state.is_loaded);
    assert_eq!(
        state.take_recovery_cover_range(300),
        Some((300, 400)),
        "the first pass acks what the recorded progress holds"
    );

    // The failed first pass found no entry after 400, so only the in-memory seq moves to 500.
    state.projected_through = state.projected_through.max(head().last_seq);
    assert_eq!(state.projected_through, 500);
    assert_eq!(state.durable_through, 400);
    assert_eq!(
        state.take_recovery_cover_range(300),
        None,
        "the second failed pass does not cover again"
    );
    assert_eq!(
        state.durable_ack_segments(vec![ack_segment(400, 3), ack_segment(500, 0)]),
        vec![ack_segment(400, 3)],
        "no ack goes above SQL progress"
    );

    state.record_committed(&outbox_batch(false, 520));
    assert_eq!(state.durable_through, 520);
    assert_eq!(
        state.durable_ack_segments(vec![ack_segment(520, 2)]),
        vec![ack_segment(520, 2)]
    );
    // The acked seq stays at or below SQL progress, so a restart continues without a load.
    let restarted = PartitionHead {
        acked_through_seq: 520,
        last_seq: 530,
        ..head()
    };
    assert_eq!(
        initial_load_reason(Some(&progress(Some(520))), &restarted),
        None
    );
}

#[test]
fn a_load_lowers_the_durable_seq_until_its_last_batch() {
    let mut state = PartitionProjection {
        recorded: Some(progress(Some(99))),
        ..PartitionProjection::default()
    };
    state.check_recorded_progress(&head());
    assert!(!state.is_loaded);
    assert_eq!(state.take_recovery_cover_range(0), None);
    state.record_committed(&outbox_batch(true, 0));
    assert_eq!(state.durable_through, 0);
    assert!(
        state
            .durable_ack_segments(vec![ack_segment(500, 3)])
            .is_empty()
    );
    state.record_committed(&outbox_batch(false, 500));
    assert_eq!(state.durable_through, 500);
}

#[tokio::test]
async fn a_panicking_or_aborted_consumer_fails_searches() {
    let panicking = new_watermarks();
    let panicking_recorder = recorder(&panicking);
    let task = tokio::spawn(async move {
        let _recorder = panicking_recorder;
        panic!("a consumer bug");
    });
    assert!(task.await.is_err_and(|error| error.is_panic()));
    assert!(matches!(
        wait(&panicking).await,
        Err(SearchError::Internal(_))
    ));

    let aborted = new_watermarks();
    let aborted_recorder = recorder(&aborted);
    let task = tokio::spawn(async move {
        let _recorder = aborted_recorder;
        std::future::pending::<()>().await;
    });
    task.abort();
    assert!(task.await.is_err_and(|error| error.is_cancelled()));
    assert!(matches!(
        wait(&aborted).await,
        Err(SearchError::Internal(_))
    ));

    let stopped = new_watermarks();
    recorder(&stopped).disarm();
    stopped.finish_seeding();
    assert!(
        wait(&stopped).await.is_ok(),
        "a requested stop is not a failure"
    );
}

fn recorder(watermarks: &Arc<SearchOutboxWatermarks>) -> UnexpectedStopRecorder {
    UnexpectedStopRecorder {
        watermarks: Some(watermarks.clone()),
    }
}

fn new_watermarks() -> Arc<SearchOutboxWatermarks> {
    Arc::new(SearchOutboxWatermarks::new(
        2,
        dexzd_metrics::NodeMetrics::unregistered().search_outbox_backlog,
    ))
}

async fn wait(watermarks: &SearchOutboxWatermarks) -> Result<(), SearchError> {
    let registry = dexzd_visibility_query::FieldRegistry::new();
    watermarks
        .wait_until_visible(SearchRequest {
            tenant_id: dexzd_keyspace::TenantId(0),
            query: "",
            page_size: 1,
            page_token: None,
            field_registry: &registry,
        })
        .await
}
