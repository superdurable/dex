// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dex_protocol::dex::{SubFlowCondition, TimerCondition, WaitingCondition};

use super::require_sub_flow_step_type;

fn sub_flow_wait() -> Option<WaitingCondition> {
    Some(WaitingCondition {
        sub_flow_conditions: vec![SubFlowCondition {
            sub_flow_type: "ChildFlow".to_string(),
            start_step_type: "StartChild".to_string(),
            ..Default::default()
        }],
        ..Default::default()
    })
}

#[test]
fn dashed_step_type_cannot_start_sub_flows() {
    let error = require_sub_flow_step_type("b-C", sub_flow_wait())
        .expect_err("a dashed Step type must not start SubFlows");

    assert_eq!(
        "Step type \"b-C\" contains \"-\" and cannot start SubFlows: the child Flow ID joins the \
         parent Flow ID, Step execution ID, and SubFlow index with \"-\", so a dash makes it \
         ambiguous. Rename the Step type.",
        error.to_string()
    );
}

#[test]
fn dash_free_step_type_starts_sub_flows() {
    let condition = require_sub_flow_step_type("StartChildren", sub_flow_wait())
        .expect("a dash-free Step type starts SubFlows")
        .expect("waiting condition");

    assert_eq!(1, condition.sub_flow_conditions.len());
}

#[test]
fn dashed_step_type_without_sub_flows_is_accepted() {
    let timer_wait = Some(WaitingCondition {
        timer_conditions: vec![TimerCondition {
            duration_seconds: 1,
            ..Default::default()
        }],
        ..Default::default()
    });

    let condition = require_sub_flow_step_type("wait-a-moment", timer_wait)
        .expect("a dashed Step type may wait without SubFlows")
        .expect("waiting condition");
    assert_eq!(1, condition.timer_conditions.len());
    assert!(
        require_sub_flow_step_type("skip-wait", None)
            .expect("skipping the wait is accepted")
            .is_none()
    );
}
