// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::marker::PhantomData;
use std::time::{Duration, SystemTime};

use crate::state::AttributeLock;
use crate::{Attribute, AttributeMap, Step, StepExecutionId, Value};

#[derive(Clone, Debug, Default, PartialEq)]
pub struct RetryPolicy {
    initial_interval: Option<Duration>,
    backoff_coefficient: Option<f64>,
    maximum_interval: Option<Duration>,
    maximum_attempts: Option<u32>,
    total_duration: Option<Duration>,
}

impl RetryPolicy {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn initial_interval(mut self, value: Duration) -> Self {
        self.initial_interval = Some(value);
        self
    }

    pub fn backoff_coefficient(mut self, value: f64) -> Self {
        self.backoff_coefficient = Some(value);
        self
    }

    pub fn maximum_interval(mut self, value: Duration) -> Self {
        self.maximum_interval = Some(value);
        self
    }

    pub fn maximum_attempts(mut self, value: u32) -> Self {
        self.maximum_attempts = Some(value);
        self
    }

    pub fn total_duration(mut self, value: Duration) -> Self {
        self.total_duration = Some(value);
        self
    }
}

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

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum IdReusePolicy {
    Default,
    AllowIfPreviousFailed,
    AllowIfNotRunning,
    Disallow,
    TerminateIfRunning,
}

#[derive(Clone, Debug)]
pub struct StartFlowOptions {
    timeout: Option<Duration>,
    start_delay: Option<Duration>,
    id_reuse_policy: IdReusePolicy,
    cron_schedule: Option<String>,
    retry_policy: Option<RetryPolicy>,
    config_override: Option<FlowConfig>,
    ignore_already_started: bool,
    request_id: Option<String>,
}

impl StartFlowOptions {
    pub fn new() -> Self {
        Self {
            timeout: None,
            start_delay: None,
            id_reuse_policy: IdReusePolicy::Default,
            cron_schedule: None,
            retry_policy: None,
            config_override: None,
            ignore_already_started: false,
            request_id: None,
        }
    }

    pub fn timeout(mut self, value: Duration) -> Self {
        self.timeout = Some(value);
        self
    }

    pub fn start_delay(mut self, value: Duration) -> Self {
        self.start_delay = Some(value);
        self
    }

    pub fn id_reuse_policy(mut self, value: IdReusePolicy) -> Self {
        self.id_reuse_policy = value;
        self
    }

    pub fn cron_schedule(mut self, value: impl Into<String>) -> Self {
        self.cron_schedule = Some(value.into());
        self
    }

    pub fn retry_policy(mut self, value: RetryPolicy) -> Self {
        self.retry_policy = Some(value);
        self
    }

    pub fn initial_attribute<T: Value>(self, _attribute: &Attribute<T>, _value: T) -> Self {
        self
    }

    pub fn initial_attribute_map<T: Value>(
        self,
        _attribute: &AttributeMap<T>,
        _instance: &str,
        _value: T,
    ) -> Self {
        self
    }

    pub fn config_override(mut self, value: FlowConfig) -> Self {
        self.config_override = Some(value);
        self
    }

    pub fn ignore_already_started(mut self, value: bool) -> Self {
        self.ignore_already_started = value;
        self
    }

    pub fn request_id(mut self, value: impl Into<String>) -> Self {
        self.request_id = Some(value.into());
        self
    }
}

impl Default for StartFlowOptions {
    fn default() -> Self {
        Self::new()
    }
}

#[derive(Clone, Debug, Default)]
pub struct FlowConfig {
    active_step_search_mode: Option<ActiveStepSearchMode>,
    continue_as_new_threshold: Option<u32>,
    continue_as_new_page_size_bytes: Option<u32>,
    step_durability: Option<StepDurability>,
    worker_target: Option<WorkerTarget>,
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

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct WorkerTarget {
    address: String,
    headless: bool,
}

impl WorkerTarget {
    pub fn new(address: impl Into<String>) -> Self {
        Self {
            address: address.into(),
            headless: false,
        }
    }

    pub fn headless(mut self, value: bool) -> Self {
        self.headless = value;
        self
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum StopType {
    Cancel,
    Terminate,
    Fail,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct StopFlowOptions {
    stop_type: StopType,
    reason: Option<String>,
}

impl StopFlowOptions {
    fn new(stop_type: StopType) -> Self {
        Self {
            stop_type,
            reason: None,
        }
    }

    pub fn cancel() -> Self {
        Self::new(StopType::Cancel)
    }

    pub fn terminate() -> Self {
        Self::new(StopType::Terminate)
    }

    pub fn fail() -> Self {
        Self::new(StopType::Fail)
    }

    pub fn reason(mut self, value: impl Into<String>) -> Self {
        self.reason = Some(value.into());
        self
    }
}

#[derive(Clone, Debug)]
pub enum ResetFlowOptions {
    Beginning,
    HistoryEventId(i64),
    HistoryEventTime(SystemTime),
    StepType(&'static str),
    StepExecution(StepExecutionId),
}

impl ResetFlowOptions {
    pub fn from_beginning() -> Self {
        Self::Beginning
    }

    pub fn from_history_event_id(event_id: i64) -> Self {
        Self::HistoryEventId(event_id)
    }

    pub fn from_history_event_time(event_time: SystemTime) -> Self {
        Self::HistoryEventTime(event_time)
    }

    pub fn from_step<SomeStep: Step>(step: &SomeStep) -> Self {
        Self::StepType(step.step_type())
    }

    pub fn from_step_execution(step_execution: StepExecutionId) -> Self {
        Self::StepExecution(step_execution)
    }
}
