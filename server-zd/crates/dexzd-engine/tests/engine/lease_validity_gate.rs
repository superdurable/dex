// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Commands are neither accepted nor acknowledged once the local lease deadline has passed.

use std::sync::Arc;

use dexzd_engine::{CommitError, CommitUrgency, LocalStoreConfig, NodeStopReason};

use crate::harness::{CountingApplier, RecordingRuntime, TestCluster};

const EXPIRED_LEASE_DETAIL: &str = "lease validity expired before renewal";

/// Moves the deadline to now, as when the lease timer has not fired yet on a stalled runtime.
fn pass_the_lease_deadline(deadline: &dexzd_cluster_metadata::LeaseValidityDeadline) {
    deadline.arm_until(tokio::time::Instant::now().into_std());
}

#[tokio::test(start_paused = true)]
async fn a_command_submitted_after_the_lease_deadline_is_refused() {
    let mut cluster = TestCluster::new();
    let test = cluster.start_in_memory().await;
    test.commit_prompt("early-flow", 1).await;
    let renewed_deadline = test
        .lease_validity_deadline
        .armed_deadline()
        .expect("a serving node holds its lease");
    pass_the_lease_deadline(&test.lease_validity_deadline);
    let refused = test.submit("late-flow", CommitUrgency::Prompt);
    assert!(
        matches!(&refused, Err(CommitError::NodeStopping { detail }) if detail.contains(EXPIRED_LEASE_DETAIL)),
        "{refused:?}"
    );
    // Only the admission check refused it; nothing has stopped the node yet.
    assert_eq!(test.node.stop_reason(), None);
    test.lease_validity_deadline.arm_until(renewed_deadline);
    test.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_frame_applied_after_the_lease_deadline_is_not_acknowledged() {
    let mut cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::InMemory);
    let runtime = Arc::new(RecordingRuntime::with_gated_frame_ends());
    let test = cluster
        .start(
            &config,
            Arc::new(CountingApplier::default()),
            runtime.clone(),
        )
        .await
        .expect("node starts");
    // Failing renewals cannot re-arm the deadline that the test moves.
    cluster.faults.fail_puts_under("lease/");
    let (_, ticket) = test
        .submit("late-flow", CommitUrgency::Prompt)
        .expect("submit succeeds while the lease is valid");
    runtime.frame_end_gate.wait_until_entered().await;
    pass_the_lease_deadline(&test.lease_validity_deadline);
    runtime.frame_end_gate.open();
    let acknowledged = ticket.wait().await;
    assert!(
        matches!(&acknowledged, Err(CommitError::NodeStopping { detail }) if detail.contains(EXPIRED_LEASE_DETAIL)),
        "{acknowledged:?}"
    );
    assert_eq!(
        runtime.committed_command_ids("late-flow").len(),
        1,
        "the command is durable and applied even though it is not acknowledged"
    );
    assert!(matches!(
        test.node.wait_for_stop().await,
        NodeStopReason::LeaseLost { detail } if detail == EXPIRED_LEASE_DETAIL
    ));
    cluster.faults.clear();
    test.node.shutdown().await.expect("lease released");
}
