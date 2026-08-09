// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::time::SystemTime;

use crate::{Attribute, AttributeMap, Channel, ChannelMap, HandlerResult, Value};

pub struct Context {
    _private: (),
}

impl Context {
    pub fn flow_id(&self) -> &str {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn run_id(&self) -> &str {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn flow_started_at(&self) -> SystemTime {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn attempt(&self) -> u32 {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn has_timer_fired(&self, _index: usize) -> bool {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn wait_for_method_failed(&self) -> bool {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn set_step_execution_local<T: Value>(
        &mut self,
        _key: &str,
        _value: T,
    ) -> HandlerResult<()> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn step_execution_local<T: Value>(&self, _key: &str) -> HandlerResult<T> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn record_event<T: Value>(&mut self, _name: &str, _value: T) -> HandlerResult<()> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn get_attribute<T: Value>(&self, _attribute: &Attribute<T>) -> HandlerResult<Option<T>> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn get_attribute_map<T: Value>(
        &self,
        _attribute: &AttributeMap<T>,
        _instance: &str,
    ) -> HandlerResult<Option<T>> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn set_attribute<T: Value>(
        &mut self,
        _attribute: &Attribute<T>,
        _value: T,
    ) -> HandlerResult<()> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn set_attribute_map<T: Value>(
        &mut self,
        _attribute: &AttributeMap<T>,
        _instance: &str,
        _value: T,
    ) -> HandlerResult<()> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn delete_attribute<T>(&mut self, _attribute: &Attribute<T>) -> HandlerResult<()> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn delete_attribute_map<T>(
        &mut self,
        _attribute: &AttributeMap<T>,
        _instance: &str,
    ) -> HandlerResult<()> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn publish<T: Value>(&mut self, _channel: &Channel<T>, _value: T) -> HandlerResult<()> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn publish_map<T: Value>(
        &mut self,
        _channel: &ChannelMap<T>,
        _instance: &str,
        _value: T,
    ) -> HandlerResult<()> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn channel_size<T>(&self, _channel: &Channel<T>) -> HandlerResult<usize> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn channel_map_size<T>(
        &self,
        _channel: &ChannelMap<T>,
        _instance: &str,
    ) -> HandlerResult<usize> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn channel_results<T: Value>(&self, _channel: &Channel<T>) -> HandlerResult<Vec<T>> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }

    pub fn channel_map_results<T: Value>(
        &self,
        _channel: &ChannelMap<T>,
        _instance: &str,
    ) -> HandlerResult<Vec<T>> {
        unimplemented!("Context belongs to the Rust Worker runtime")
    }
}
