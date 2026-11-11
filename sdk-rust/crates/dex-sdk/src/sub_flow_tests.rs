// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dex_protocol::dex::{SubFlowCondition, TimerCondition, WaitingCondition};

use super::warn_dashed_sub_flow_step_type;

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
fn dashed_step_type_sends_sub_flows_unchanged_for_the_server_to_reject() {
    let condition = warn_dashed_sub_flow_step_type("b-C", sub_flow_wait());

    assert_eq!(sub_flow_wait(), condition);
}

#[test]
fn dash_free_step_type_sends_sub_flows_unchanged() {
    let condition = warn_dashed_sub_flow_step_type("StartChildren", sub_flow_wait());

    assert_eq!(sub_flow_wait(), condition);
}

#[test]
fn dashed_step_type_without_sub_flows_is_sent_unchanged() {
    let timer_wait = Some(WaitingCondition {
        timer_conditions: vec![TimerCondition {
            duration_seconds: 1,
            ..Default::default()
        }],
        ..Default::default()
    });

    assert_eq!(
        timer_wait.clone(),
        warn_dashed_sub_flow_step_type("wait-a-moment", timer_wait)
    );
    assert!(warn_dashed_sub_flow_step_type("skip-wait", None).is_none());
}
