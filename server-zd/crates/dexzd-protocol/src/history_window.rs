// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The unlimited history window that `FlowConfig.history_window_events` requests with 0.

use crate::dex::{FlowConfig, WaitingCondition};

/// Recorded unlimited FlowConfig override. Older appliers treat it as finite.
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
