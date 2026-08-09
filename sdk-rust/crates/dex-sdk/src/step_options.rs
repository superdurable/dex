// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::marker::PhantomData;
use std::time::Duration;

use crate::attribute::AttributeLock;
use crate::{RetryPolicy, Step, Value};

pub struct StepOptions<Input> {
    wait_for_method_timeout: Option<Duration>,
    execute_method_timeout: Option<Duration>,
    wait_for_retry: Option<RetryPolicy>,
    execute_retry: Option<RetryPolicy>,
    wait_for_failure: WaitForFailurePolicy,
    wait_for_durability: StepDurability,
    execute_durability: StepDurability,
    wait_for_locks: Vec<AttributeLock>,
    execute_locks: Vec<AttributeLock>,
    execute_failure_step: Option<&'static str>,
    marker: PhantomData<fn(Input)>,
}

impl<Input: Value> StepOptions<Input> {
    pub fn new() -> Self {
        Self {
            wait_for_method_timeout: None,
            execute_method_timeout: None,
            wait_for_retry: None,
            execute_retry: None,
            wait_for_failure: WaitForFailurePolicy::FailFlow,
            wait_for_durability: StepDurability::Default,
            execute_durability: StepDurability::Default,
            wait_for_locks: Vec::new(),
            execute_locks: Vec::new(),
            execute_failure_step: None,
            marker: PhantomData,
        }
    }

    pub fn wait_for_method_timeout(mut self, value: Duration) -> Self {
        self.wait_for_method_timeout = Some(value);
        self
    }

    pub fn execute_method_timeout(mut self, value: Duration) -> Self {
        self.execute_method_timeout = Some(value);
        self
    }

    pub fn wait_for_retry(mut self, value: RetryPolicy) -> Self {
        self.wait_for_retry = Some(value);
        self
    }

    pub fn execute_retry(mut self, value: RetryPolicy) -> Self {
        self.execute_retry = Some(value);
        self
    }

    pub fn wait_for_failure(mut self, value: WaitForFailurePolicy) -> Self {
        self.wait_for_failure = value;
        self
    }

    pub fn on_execute_failure_proceed_to<RecoveryStep>(mut self, step: &RecoveryStep) -> Self
    where
        RecoveryStep: Step<Input = Input>,
    {
        self.execute_failure_step = Some(step.step_type());
        self
    }

    pub fn wait_for_durability(mut self, value: StepDurability) -> Self {
        self.wait_for_durability = value;
        self
    }

    pub fn execute_durability(mut self, value: StepDurability) -> Self {
        self.execute_durability = value;
        self
    }

    pub fn wait_for_lock(mut self, value: AttributeLock) -> Self {
        self.wait_for_locks.push(value);
        self
    }

    pub fn execute_lock(mut self, value: AttributeLock) -> Self {
        self.execute_locks.push(value);
        self
    }
}

impl<Input: Value> Default for StepOptions<Input> {
    fn default() -> Self {
        Self::new()
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum WaitForFailurePolicy {
    FailFlow,
    Proceed,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum StepDurability {
    Default,
    Sync,
    Async,
}
