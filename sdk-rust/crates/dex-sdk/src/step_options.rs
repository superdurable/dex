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
    pub(crate) wait_for_method_timeout: Option<Duration>,
    pub(crate) execute_method_timeout: Option<Duration>,
    pub(crate) wait_for_retry: Option<RetryPolicy>,
    pub(crate) execute_retry: Option<RetryPolicy>,
    pub(crate) wait_for_failure: WaitForFailurePolicy,
    pub(crate) wait_for_durability: StepDurability,
    pub(crate) execute_durability: StepDurability,
    pub(crate) wait_for_locks: Vec<AttributeLock>,
    pub(crate) execute_locks: Vec<AttributeLock>,
    pub(crate) execute_failure_step: Option<&'static str>,
    marker: PhantomData<fn(Input)>,
}

#[derive(Clone)]
pub(crate) struct ErasedStepOptions {
    pub(crate) wait_for_method_timeout: Option<Duration>,
    pub(crate) execute_method_timeout: Option<Duration>,
    pub(crate) wait_for_retry: Option<RetryPolicy>,
    pub(crate) execute_retry: Option<RetryPolicy>,
    pub(crate) wait_for_failure: WaitForFailurePolicy,
    pub(crate) wait_for_durability: StepDurability,
    pub(crate) execute_durability: StepDurability,
    pub(crate) wait_for_locks: Vec<AttributeLock>,
    pub(crate) execute_locks: Vec<AttributeLock>,
    pub(crate) execute_failure_step: Option<&'static str>,
}

impl<Input> From<StepOptions<Input>> for ErasedStepOptions {
    fn from(options: StepOptions<Input>) -> Self {
        Self {
            wait_for_method_timeout: options.wait_for_method_timeout,
            execute_method_timeout: options.execute_method_timeout,
            wait_for_retry: options.wait_for_retry,
            execute_retry: options.execute_retry,
            wait_for_failure: options.wait_for_failure,
            wait_for_durability: options.wait_for_durability,
            execute_durability: options.execute_durability,
            wait_for_locks: options.wait_for_locks,
            execute_locks: options.execute_locks,
            execute_failure_step: options.execute_failure_step,
        }
    }
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
