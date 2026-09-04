// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::time::Duration;

use crate::attribute::{AttributeLock, AttributeMapLoad};
use crate::channel::{ChannelLoad, ChannelMapLoad};
use crate::step_options::ErasedStepOptions;
use crate::{AttributeMap, Channel, ChannelMap, RetryPolicy, Step, StepDurability, StepOptions};

/// Routes exhausted Flow timeout-handler retries to an input-free Step.
#[derive(Clone)]
pub struct FlowTimeoutHandlerFailure {
    pub(crate) step_type: &'static str,
    pub(crate) options: Option<ErasedStepOptions>,
}

impl FlowTimeoutHandlerFailure {
    /// Routes failure to `step` using its registered options.
    ///
    /// The Step receives unit input and reads the terminal handler error from
    /// [`Context::recovery_error`](crate::Context::recovery_error).
    pub fn proceed_to<RecoveryStep>(step: &RecoveryStep) -> Self
    where
        RecoveryStep: Step<Input = ()>,
    {
        Self {
            step_type: step.step_type(),
            options: None,
        }
    }

    /// Routes failure to `step` with movement-specific Step options.
    pub fn proceed_to_with_options<RecoveryStep>(
        step: &RecoveryStep,
        options: StepOptions<()>,
    ) -> Self
    where
        RecoveryStep: Step<Input = ()>,
    {
        Self {
            step_type: step.step_type(),
            options: Some(options.into()),
        }
    }
}

/// Configures execution and selective state loading for a Flow timeout handler.
///
/// Durations start when the soft Flow timeout fires. Omitted fields preserve the server's Execute
/// defaults. AttributeMap contents and pending Channel messages are loaded only when selected.
#[derive(Clone)]
pub struct FlowTimeoutHandlerOptions {
    pub(crate) method_timeout: Option<Duration>,
    pub(crate) heartbeat_timeout: Option<Duration>,
    pub(crate) retry: Option<RetryPolicy>,
    pub(crate) failure: Option<FlowTimeoutHandlerFailure>,
    pub(crate) durability: StepDurability,
    pub(crate) locks: Vec<AttributeLock>,
    pub(crate) load_attribute_maps: Vec<AttributeMapLoad>,
    pub(crate) load_channels: Vec<ChannelLoad>,
    pub(crate) load_channel_maps: Vec<ChannelMapLoad>,
}

impl FlowTimeoutHandlerOptions {
    /// Creates timeout-handler options that preserve server defaults.
    pub fn new() -> Self {
        Self {
            method_timeout: None,
            heartbeat_timeout: None,
            retry: None,
            failure: None,
            durability: StepDurability::Default,
            locks: Vec::new(),
            load_attribute_maps: Vec::new(),
            load_channels: Vec::new(),
            load_channel_maps: Vec::new(),
        }
    }

    /// Sets the maximum duration of one timeout-handler attempt.
    pub fn method_timeout(mut self, value: Duration) -> Self {
        self.method_timeout = Some(value);
        self
    }

    /// Sets the timeout for progress heartbeats from the timeout handler.
    pub fn heartbeat_timeout(mut self, value: Duration) -> Self {
        self.heartbeat_timeout = Some(value);
        self
    }

    /// Sets retry behavior for the timeout handler's logical execution.
    pub fn retry(mut self, value: RetryPolicy) -> Self {
        self.retry = Some(value);
        self
    }

    /// Sets routing after all timeout-handler attempts fail.
    pub fn failure(mut self, value: FlowTimeoutHandlerFailure) -> Self {
        self.failure = Some(value);
        self
    }

    /// Overrides persistence durability for the timeout-handler response.
    pub fn durability(mut self, value: StepDurability) -> Self {
        self.durability = value;
        self
    }

    /// Adds an Attribute lock held for each timeout-handler attempt.
    pub fn lock(mut self, value: AttributeLock) -> Self {
        self.locks.push(value);
        self
    }

    /// Loads every current instance of `attribute_map` for the timeout handler.
    pub fn load_attribute_map<T>(mut self, attribute_map: &AttributeMap<T>) -> Self {
        self.load_attribute_maps.push(AttributeMapLoad {
            name: attribute_map.name().to_owned(),
            instance: None,
        });
        self
    }

    /// Loads one AttributeMap instance for the timeout handler.
    pub fn load_attribute_map_instance(mut self, load: AttributeMapLoad) -> Self {
        self.load_attribute_maps.push(load);
        self
    }

    /// Loads one Channel's pending messages for the timeout handler.
    pub fn load_channel<T>(mut self, channel: &Channel<T>) -> Self {
        self.load_channels.push(ChannelLoad {
            name: channel.name().to_owned(),
        });
        self
    }

    /// Loads every current ChannelMap instance's pending messages for the timeout handler.
    pub fn load_channel_map<T>(mut self, channel_map: &ChannelMap<T>) -> Self {
        self.load_channel_maps.push(ChannelMapLoad {
            name: channel_map.name().to_owned(),
            instance: None,
        });
        self
    }

    /// Loads one ChannelMap instance's pending messages for the timeout handler.
    pub fn load_channel_map_instance(mut self, load: ChannelMapLoad) -> Self {
        self.load_channel_maps.push(load);
        self
    }
}

impl Default for FlowTimeoutHandlerOptions {
    fn default() -> Self {
        Self::new()
    }
}
