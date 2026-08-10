// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use crate::{StepDurability, WorkerTarget};

#[derive(Clone, Debug, Default)]
pub struct FlowConfig {
    pub(crate) active_step_search_mode: Option<ActiveStepSearchMode>,
    pub(crate) continue_as_new_threshold: Option<u32>,
    pub(crate) continue_as_new_page_size_bytes: Option<u32>,
    pub(crate) step_durability: Option<StepDurability>,
    pub(crate) worker_target: Option<WorkerTarget>,
}

impl FlowConfig {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn active_step_search_mode(mut self, value: ActiveStepSearchMode) -> Self {
        self.active_step_search_mode = Some(value);
        self
    }

    pub fn continue_as_new_threshold(mut self, value: u32) -> Self {
        self.continue_as_new_threshold = Some(value);
        self
    }

    pub fn continue_as_new_page_size_bytes(mut self, value: u32) -> Self {
        self.continue_as_new_page_size_bytes = Some(value);
        self
    }

    pub fn step_durability(mut self, value: StepDurability) -> Self {
        self.step_durability = Some(value);
        self
    }

    pub fn worker_target(mut self, value: WorkerTarget) -> Self {
        self.worker_target = Some(value);
        self
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ActiveStepSearchMode {
    All,
    WithWaitFor,
    Disabled,
}
