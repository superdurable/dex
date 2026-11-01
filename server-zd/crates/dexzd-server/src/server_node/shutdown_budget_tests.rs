// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::*;

#[test]
fn the_step_budgets_total_the_documented_shutdown_budget() {
    assert_eq!(SHUTDOWN_BUDGET, Duration::from_secs(15));
}

#[tokio::test(start_paused = true)]
async fn a_slow_embedded_s3_close_uses_the_budget_that_earlier_steps_left() {
    let mut teardown = BoundedTeardown::start();
    teardown.run("engine", ENGINE_STOP_BUDGET, async {}).await;
    teardown
        .run(
            "embedded S3",
            embedded_s3_stop_budget(&teardown),
            tokio::time::sleep(Duration::from_secs(3)),
        )
        .await;
    let finished = teardown.finish(Ok(()));
    assert!(finished.is_ok(), "{finished:?}");
}

#[tokio::test(start_paused = true)]
async fn a_stalled_embedded_s3_ends_at_the_shutdown_budget_or_after_its_minimum() {
    let slowest_earlier_steps = SHUTDOWN_BUDGET.saturating_sub(EMBEDDED_S3_MINIMUM_STOP_BUDGET);
    for earlier_steps in [
        Duration::ZERO,
        Duration::from_secs(5),
        slowest_earlier_steps,
        Duration::from_secs(14),
    ] {
        let started = tokio::time::Instant::now();
        let mut teardown = BoundedTeardown::start();
        tokio::time::sleep(earlier_steps).await;
        let budget = embedded_s3_stop_budget(&teardown);
        teardown
            .run("embedded S3", budget, std::future::pending())
            .await;
        assert_eq!(
            started.elapsed(),
            SHUTDOWN_BUDGET.max(earlier_steps + EMBEDDED_S3_MINIMUM_STOP_BUDGET),
            "after {earlier_steps:?}"
        );
        let expected = format!("embedded S3 exceeded its {} ms budget", budget.as_millis());
        assert!(
            matches!(
                teardown.finish(Ok(())),
                Err(ServerError::ShutdownIncomplete { steps }) if steps == expected
            ),
            "after {earlier_steps:?}"
        );
    }
}
