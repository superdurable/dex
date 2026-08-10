// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::collections::BTreeMap;
use std::time::SystemTime;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum FlowStatus {
    Running,
    Completed,
    Failed,
    TimedOut,
    Terminated,
    Canceled,
    ContinuedAsNew,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum FlowErrorType {
    StepDecisionFailed,
    ClientApiFailed,
    WorkerApiFailed,
    InvalidUserFlowCode,
    Internal,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FlowInfo {
    pub flow_id: String,
    pub run_id: String,
    pub flow_type: String,
    pub status: FlowStatus,
    pub started_at: SystemTime,
}

#[derive(Clone, Debug, PartialEq)]
pub struct SearchFlowEntry {
    pub flow_id: String,
    pub run_id: String,
    pub flow_type: String,
    pub status: FlowStatus,
    pub started_at: Option<SystemTime>,
    pub closed_at: Option<SystemTime>,
    pub search_attributes: BTreeMap<String, serde_json::Value>,
}

#[derive(Clone, Debug, PartialEq)]
pub struct SearchFlowsPage {
    pub flows: Vec<SearchFlowEntry>,
    pub next_page_token: String,
}
