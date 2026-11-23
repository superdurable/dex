// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The unlimited history window that `FlowConfig.history_window_events` requests with 0.

use crate::dex::{FlowConfig, WaitingCondition};

/// The `history_window_events` that commands and runs record for an unlimited window.
///
/// Servers before 0.1.38 read it as a window larger than any run; a recorded 0 would fail their
/// apply.
pub const RECORDED_UNLIMITED_HISTORY_WINDOW_EVENTS: i32 = i32::MAX;

/// Returns whether `window_events` keeps the whole history: 0 in a request, or its recorded value.
pub fn is_unlimited_history_window_events(window_events: i32) -> bool {
    window_events == 0 || window_events == RECORDED_UNLIMITED_HISTORY_WINDOW_EVENTS
}

/// Replaces an unlimited window of 0 with [`RECORDED_UNLIMITED_HISTORY_WINDOW_EVENTS`].
pub fn record_unlimited_history_window(flow_config: &mut FlowConfig) {
    if flow_config.history_window_events == Some(0) {
        flow_config.history_window_events = Some(RECORDED_UNLIMITED_HISTORY_WINDOW_EVENTS);
    }
}

/// Applies [`record_unlimited_history_window`] to every SubFlow override of `waiting`.
pub fn record_unlimited_sub_flow_history_windows(waiting: &mut WaitingCondition) {
    for flow_config in waiting
        .sub_flow_conditions
        .iter_mut()
        .filter_map(|condition| condition.options.as_mut())
        .filter_map(|options| options.flow_config_override.as_mut())
    {
        record_unlimited_history_window(flow_config);
    }
}

#[cfg(test)]
mod tests {
    use crate::dex::{SubFlowCondition, SubFlowOptions};

    use super::*;

    fn window_config(window_events: Option<i32>) -> FlowConfig {
        FlowConfig {
            history_window_events: window_events,
            ..FlowConfig::default()
        }
    }

    #[test]
    fn only_zero_becomes_the_recorded_unlimited_window() {
        for (requested, recorded) in [
            (None, None),
            (Some(0), Some(i32::MAX)),
            (Some(1), Some(1)),
            (Some(250), Some(250)),
            (Some(i32::MAX), Some(i32::MAX)),
        ] {
            let mut flow_config = window_config(requested);
            record_unlimited_history_window(&mut flow_config);
            assert_eq!(flow_config.history_window_events, recorded, "{requested:?}");
        }
        assert!(is_unlimited_history_window_events(0));
        assert!(is_unlimited_history_window_events(i32::MAX));
        assert!(!is_unlimited_history_window_events(1));
        assert!(!is_unlimited_history_window_events(-1));
    }

    #[test]
    fn every_sub_flow_override_records_the_unlimited_window() {
        let condition = |flow_config_override| SubFlowCondition {
            options: Some(SubFlowOptions {
                flow_config_override,
                ..SubFlowOptions::default()
            }),
            ..SubFlowCondition::default()
        };
        let mut waiting = WaitingCondition {
            sub_flow_conditions: vec![
                condition(Some(window_config(Some(0)))),
                condition(Some(window_config(Some(30)))),
                condition(None),
                SubFlowCondition::default(),
            ],
            ..WaitingCondition::default()
        };
        record_unlimited_sub_flow_history_windows(&mut waiting);
        let recorded: Vec<_> = waiting
            .sub_flow_conditions
            .iter()
            .map(|condition| {
                condition
                    .options
                    .as_ref()
                    .and_then(|options| options.flow_config_override.as_ref())
                    .and_then(|flow_config| flow_config.history_window_events)
            })
            .collect();
        assert_eq!(recorded, [Some(i32::MAX), Some(30), None, None]);
    }
}
