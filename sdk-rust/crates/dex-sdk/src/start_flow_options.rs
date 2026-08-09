// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::time::Duration;

use crate::{Attribute, AttributeMap, FlowConfig, RetryPolicy, Value};

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
