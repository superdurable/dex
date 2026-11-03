// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Attempt progress across retries: it never decreases, and a marked attempt counts as started.

use dexzd_protocol::dex::StepAttemptProgress;
use dexzd_protocol::dexzd::v1::{
    StepAttemptFailedCommand, StepInvocationPhase, StepInvocationRecord,
};

use crate::invariants::InvariantViolation;

/// Checks the retry that an applied attempt failure scheduled, if any.
///
/// Returns a violation when the stored furthest progress ranks below the failed attempt's own,
/// below the earlier attempts', or below `Started` for an attempt its Worker marked.
pub(crate) fn check_retried_attempt_progress(
    failure: &StepAttemptFailedCommand,
    before: Option<&StepInvocationRecord>,
    after: Option<&StepInvocationRecord>,
) -> Result<(), InvariantViolation> {
    let (Some(before), Some(after)) = (before, after) else {
        return Ok(());
    };
    let is_retry =
        after.phase == StepInvocationPhase::Backoff as i32 && after.attempt == failure.attempt + 1;
    if !is_retry {
        return Ok(());
    }
    let stored = rank(after.furthest_attempt_progress);
    let is_marked = failure.attempt > 0 && before.marked_started_attempt == failure.attempt;
    let earlier = if failure.attempt > 1 {
        rank(before.furthest_attempt_progress)
    } else {
        0
    };
    let minimum = if is_marked {
        rank(StepAttemptProgress::Started as i32)
    } else {
        rank(failure.attempt_progress).max(earlier)
    };
    if stored < minimum {
        return Err(InvariantViolation::new(
            "P1-attempt-progress",
            format!(
                "attempt {} stored progress {} below {minimum} (marked: {is_marked})",
                failure.attempt, after.furthest_attempt_progress
            ),
        ));
    }
    Ok(())
}

/// Ranks progress by how likely the work ran; unknown counts as possibly run.
fn rank(progress: i32) -> u8 {
    match StepAttemptProgress::try_from(progress) {
        Ok(StepAttemptProgress::NotAssigned) => 0,
        Ok(StepAttemptProgress::Assigned) => 1,
        Ok(StepAttemptProgress::Started) => 3,
        Ok(StepAttemptProgress::Unspecified) | Err(_) => 2,
    }
}
