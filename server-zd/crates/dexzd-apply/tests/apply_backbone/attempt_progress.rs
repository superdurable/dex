// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::*;
use dexzd_protocol::dex::StepAttemptProgress;
use dexzd_protocol::dexzd::v1::StepAttemptMarkedStartedCommand;

const MAXIMUM_ATTEMPTS: i32 = 2;

/// Starts a Flow whose Execute retries once, then proceeds to `RecoverStep`.
fn start_with_recovery(harness: &mut Harness) {
    let mut request = start_request("request-1");
    request.step_options = Some(StepOptions {
        skip_wait_for: true,
        execute_retry_policy: Some(RetryPolicy {
            maximum_attempts: MAXIMUM_ATTEMPTS,
            ..RetryPolicy::default()
        }),
        execute_failure_policy: ExecuteMethodFailurePolicy::ProceedToConfiguredStep as i32,
        execute_failure_proceed_step_type: "RecoverStep".to_owned(),
        execute_failure_proceed_step_options: Some(Box::new(StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        })),
        ..StepOptions::default()
    });
    harness.start(request);
}

fn mark(harness: &mut Harness, attempt: u32) -> ApplyOutcome {
    harness.apply(Command::StepAttemptMarkedStarted(
        StepAttemptMarkedStartedCommand {
            invocation: Some(key("ChargeStep-1", StepMethod::Execute)),
            attempt,
        },
    ))
}

fn fail(harness: &mut Harness, attempt: u32, progress: StepAttemptProgress) -> ApplyOutcome {
    harness.apply(Command::StepAttemptFailed(StepAttemptFailedCommand {
        invocation: Some(key("ChargeStep-1", StepMethod::Execute)),
        attempt,
        kind: StepAttemptFailureKind::MethodTimeout as i32,
        server_detail: "method timeout of 5s expired".to_owned(),
        attempt_progress: progress as i32,
        ..StepAttemptFailedCommand::default()
    }))
}

fn fire_retry(harness: &mut Harness, next_attempt: u32) {
    harness.apply(Command::TimersFired(TimersFiredCommand {
        timers: vec![TimerId {
            run_attempt: 1,
            kind: Some(timer_id::Kind::RetryBackoff(RetryBackoffTimerId {
                invocation: Some(key("ChargeStep-1", StepMethod::Execute)),
                next_attempt,
            })),
        }],
    }));
}

fn recovery_progress(harness: &Harness) -> StepAttemptProgress {
    let recovery_error = harness
        .step_execution("RecoverStep-1")
        .recovery_error
        .expect("the recovery Step carries the error");
    StepAttemptProgress::try_from(recovery_error.attempt_progress).expect("a known progress")
}

/// Runs attempt 1 to a failure with `first`, then attempt 2 to a failure with `second`.
fn recover_after_two_attempts(
    first: (bool, StepAttemptProgress),
    second: (bool, StepAttemptProgress),
) -> StepAttemptProgress {
    let mut harness = Harness::new("order-1");
    start_with_recovery(&mut harness);
    for (attempt, (is_marked, progress)) in [(1, first), (2, second)] {
        if attempt == 2 {
            fire_retry(&mut harness, 2);
        }
        harness.start_attempt("ChargeStep-1", StepMethod::Execute, attempt);
        if is_marked {
            mark(&mut harness, attempt);
        }
        fail(&mut harness, attempt, progress);
    }
    recovery_progress(&harness)
}

#[test]
fn a_marked_attempt_reaches_recovery_as_started_whatever_the_owner_reported() {
    // The mark commits while the method deadline fires, so the owner still reports NOT_ASSIGNED.
    assert_eq!(
        recover_after_two_attempts(
            (true, StepAttemptProgress::NotAssigned),
            (true, StepAttemptProgress::NotAssigned)
        ),
        StepAttemptProgress::Started
    );
}

#[test]
fn recovery_sees_the_furthest_progress_of_all_attempts() {
    use StepAttemptProgress::{Assigned, NotAssigned, Started, Unspecified};
    assert_eq!(
        recover_after_two_attempts((true, Started), (false, NotAssigned)),
        Started
    );
    assert_eq!(
        recover_after_two_attempts((false, Assigned), (false, NotAssigned)),
        Assigned
    );
    assert_eq!(
        recover_after_two_attempts((false, NotAssigned), (false, NotAssigned)),
        NotAssigned
    );
    // Unknown progress means the work may have run, so it outranks ASSIGNED.
    assert_eq!(
        recover_after_two_attempts((false, Unspecified), (false, Assigned)),
        Unspecified
    );
    assert_eq!(
        recover_after_two_attempts((false, NotAssigned), (true, Started)),
        Started
    );
}

#[test]
fn an_earlier_attempt_without_progress_keeps_the_route_at_unspecified() {
    // An older engine reported no progress, so the earlier attempt may have run.
    assert_eq!(
        recover_after_two_attempts(
            (false, StepAttemptProgress::Unspecified),
            (false, StepAttemptProgress::NotAssigned)
        ),
        StepAttemptProgress::Unspecified
    );
}

#[test]
fn a_mark_for_an_attempt_that_is_not_running_is_rejected_without_writes() {
    let mut harness = Harness::new("order-1");
    start_with_recovery(&mut harness);
    harness.start_attempt("ChargeStep-1", StepMethod::Execute, 1);
    let stale = mark(&mut harness, 2);
    assert!(stale.writes.is_empty());
    assert!(is_error(&stale.reply, 9));
    let accepted = mark(&mut harness, 1);
    assert!(!accepted.writes.is_empty());
    assert_eq!(
        harness
            .step_execution("ChargeStep-1")
            .invocation
            .expect("the attempt runs")
            .marked_started_attempt,
        1
    );
}
