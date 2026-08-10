// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::collections::{HashMap, HashSet};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use std::time::{Duration, SystemTime};

use dex_protocol::dex::{
    AttributeWrite, ChannelInfo, ChannelMessage, ConditionResults, ConditionStatus,
    Context as ProtoContext, Kv, Value as ProtoValue, value,
};

use crate::persistence::PersistenceKind;
use crate::registry::{RegisteredFlow, physical_name};
use crate::value_mapper;
use crate::{Attribute, AttributeMap, Channel, ChannelMap, HandlerError, HandlerResult, Value};

#[derive(Clone, Copy, Eq, PartialEq)]
pub(crate) enum InvocationMethod {
    WaitFor,
    Execute,
    Rpc,
}

pub struct Context {
    method: InvocationMethod,
    flow: RegisteredFlow,
    metadata: ProtoContext,
    attributes: HashMap<String, ProtoValue>,
    locals: HashMap<String, ProtoValue>,
    condition_results: Option<ConditionResults>,
    channel_infos: HashMap<String, ChannelInfo>,
    attribute_writes: HashMap<String, AttributeWrite>,
    local_writes: HashMap<String, Kv>,
    events: Vec<Kv>,
    event_names: HashSet<String>,
    publications: Vec<ChannelMessage>,
    cancellation: InvocationCancellation,
}

impl Context {
    pub fn flow_id(&self) -> &str {
        &self.metadata.flow_id
    }

    pub fn run_id(&self) -> &str {
        &self.metadata.run_id
    }

    pub fn flow_started_at(&self) -> SystemTime {
        system_time(self.metadata.flow_started_timestamp)
    }

    pub fn step_execution_id(&self) -> &str {
        &self.metadata.step_execution_id
    }

    pub fn from_step_execution_id(&self) -> &str {
        &self.metadata.from_step_execution_id
    }

    pub fn first_attempt_at(&self) -> SystemTime {
        system_time(self.metadata.first_attempt_timestamp)
    }

    pub fn attempt(&self) -> u32 {
        u32::try_from(self.metadata.attempt).unwrap_or_default()
    }

    pub fn has_any_timer_fired(&self) -> bool {
        self.condition_results.as_ref().is_some_and(|results| {
            results
                .timer_results
                .iter()
                .any(|timer| timer.condition_status == ConditionStatus::Completed as i32)
        })
    }

    pub fn has_timer_fired(&self, index: usize) -> bool {
        self.condition_results
            .as_ref()
            .and_then(|results| results.timer_results.get(index))
            .is_some_and(|timer| timer.condition_status == ConditionStatus::Completed as i32)
    }

    pub fn wait_for_method_failed(&self) -> bool {
        self.condition_results
            .as_ref()
            .is_some_and(|results| results.wait_for_failed)
    }

    pub fn wait_for_cancellation(&self) {
        self.cancellation.wait();
    }

    pub fn is_cancelled(&self) -> bool {
        self.cancellation.is_cancelled()
    }

    pub fn set_step_execution_local<T: Value>(&mut self, key: &str, value: T) -> HandlerResult<()> {
        require_name(key, "step-execution local key")?;
        let value = value_mapper::encode_handler(&value)?;
        self.local_writes.insert(
            key.to_string(),
            Kv {
                key: key.to_string(),
                value: Some(value),
            },
        );
        Ok(())
    }

    pub fn step_execution_local<T: Value>(&self, key: &str) -> HandlerResult<T> {
        require_name(key, "step-execution local key")?;
        let value = self
            .local_writes
            .get(key)
            .and_then(|entry| entry.value.as_ref())
            .or_else(|| self.locals.get(key))
            .ok_or_else(|| HandlerError::new(format!("step-execution local {key} is missing")))?;
        value_mapper::decode_handler(value)
    }

    pub fn record_event<T: Value>(&mut self, name: &str, value: T) -> HandlerResult<()> {
        require_name(name, "event name")?;
        if !self.event_names.insert(name.to_string()) {
            return Err(HandlerError::new(format!(
                "event was already recorded: {name}"
            )));
        }
        self.events.push(Kv {
            key: name.to_string(),
            value: Some(value_mapper::encode_handler(&value)?),
        });
        Ok(())
    }

    pub fn get_attribute<T: Value>(&self, attribute: &Attribute<T>) -> HandlerResult<Option<T>> {
        self.get_attribute_value(attribute.name(), PersistenceKind::Attribute, None)
    }

    pub fn get_attribute_map<T: Value>(
        &self,
        attribute: &AttributeMap<T>,
        instance: &str,
    ) -> HandlerResult<Option<T>> {
        self.get_attribute_value(
            attribute.name(),
            PersistenceKind::AttributeMap,
            Some(instance),
        )
    }

    pub fn set_attribute<T: Value>(
        &mut self,
        attribute: &Attribute<T>,
        value: T,
    ) -> HandlerResult<()> {
        self.set_attribute_value(
            attribute.name(),
            PersistenceKind::Attribute,
            None,
            value_mapper::encode_handler(&value)?,
            attribute.index().map(|index| index.proto_config(false)),
        )
    }

    pub fn set_attribute_map<T: Value>(
        &mut self,
        attribute: &AttributeMap<T>,
        instance: &str,
        value: T,
    ) -> HandlerResult<()> {
        self.set_attribute_value(
            attribute.name(),
            PersistenceKind::AttributeMap,
            Some(instance),
            value_mapper::encode_handler(&value)?,
            attribute.index().map(|index| index.proto_config(true)),
        )
    }

    pub fn delete_attribute<T>(&mut self, attribute: &Attribute<T>) -> HandlerResult<()> {
        self.set_attribute_value(
            attribute.name(),
            PersistenceKind::Attribute,
            None,
            value_mapper::deletion(),
            attribute.index().map(|index| index.proto_config(false)),
        )
    }

    pub fn delete_attribute_map<T>(
        &mut self,
        attribute: &AttributeMap<T>,
        instance: &str,
    ) -> HandlerResult<()> {
        self.set_attribute_value(
            attribute.name(),
            PersistenceKind::AttributeMap,
            Some(instance),
            value_mapper::deletion(),
            attribute.index().map(|index| index.proto_config(true)),
        )
    }

    pub fn publish<T: Value>(&mut self, channel: &Channel<T>, value: T) -> HandlerResult<()> {
        self.publish_value(
            channel.name(),
            PersistenceKind::Channel,
            None,
            value_mapper::encode_handler(&value)?,
        )
    }

    pub fn publish_map<T: Value>(
        &mut self,
        channel: &ChannelMap<T>,
        instance: &str,
        value: T,
    ) -> HandlerResult<()> {
        self.publish_value(
            channel.name(),
            PersistenceKind::ChannelMap,
            Some(instance),
            value_mapper::encode_handler(&value)?,
        )
    }

    pub fn channel_size<T>(&self, channel: &Channel<T>) -> HandlerResult<usize> {
        self.channel_size_value(channel.name(), PersistenceKind::Channel, None)
    }

    pub fn channel_map_size<T>(
        &self,
        channel: &ChannelMap<T>,
        instance: &str,
    ) -> HandlerResult<usize> {
        self.channel_size_value(channel.name(), PersistenceKind::ChannelMap, Some(instance))
    }

    pub fn channel_results<T: Value>(&self, channel: &Channel<T>) -> HandlerResult<Vec<T>> {
        self.channel_results_value(channel.name(), PersistenceKind::Channel, None)
    }

    pub fn channel_map_results<T: Value>(
        &self,
        channel: &ChannelMap<T>,
        instance: &str,
    ) -> HandlerResult<Vec<T>> {
        self.channel_results_value(channel.name(), PersistenceKind::ChannelMap, Some(instance))
    }

    pub(crate) fn new(
        method: InvocationMethod,
        flow: RegisteredFlow,
        metadata: ProtoContext,
        attributes: Vec<Kv>,
        locals: Vec<Kv>,
        condition_results: Option<ConditionResults>,
        channel_infos: HashMap<String, ChannelInfo>,
    ) -> HandlerResult<Self> {
        Ok(Self {
            method,
            flow,
            metadata,
            attributes: map_values("Attribute", attributes)?,
            locals: map_values("step-execution local", locals)?,
            condition_results,
            channel_infos,
            attribute_writes: HashMap::new(),
            local_writes: HashMap::new(),
            events: Vec::new(),
            event_names: HashSet::new(),
            publications: Vec::new(),
            cancellation: InvocationCancellation::new(),
        })
    }

    pub(crate) fn cancellation(&self) -> InvocationCancellation {
        self.cancellation.clone()
    }

    pub(crate) fn take_outputs(
        self,
    ) -> (Vec<AttributeWrite>, Vec<Kv>, Vec<Kv>, Vec<ChannelMessage>) {
        (
            self.attribute_writes.into_values().collect(),
            self.local_writes.into_values().collect(),
            self.events,
            self.publications,
        )
    }

    fn get_attribute_value<T: Value>(
        &self,
        name: &str,
        kind: PersistenceKind,
        instance: Option<&str>,
    ) -> HandlerResult<Option<T>> {
        let key = self.registered_name(name, kind, instance)?;
        if let Some(write) = self.attribute_writes.get(&key) {
            let value = write
                .value
                .as_ref()
                .ok_or_else(|| HandlerError::new(format!("Attribute write {key} has no Value")))?;
            if matches!(value.kind, Some(value::Kind::NullValue(_))) {
                return Ok(None);
            }
            return value_mapper::decode_handler(value).map(Some);
        }
        self.attributes
            .get(&key)
            .map(value_mapper::decode_handler)
            .transpose()
    }

    fn set_attribute_value(
        &mut self,
        name: &str,
        kind: PersistenceKind,
        instance: Option<&str>,
        value: ProtoValue,
        index_config: Option<dex_protocol::dex::IndexConfig>,
    ) -> HandlerResult<()> {
        let key = self.registered_name(name, kind, instance)?;
        self.attribute_writes.insert(
            key.clone(),
            AttributeWrite {
                key,
                value: Some(value),
                index_config,
            },
        );
        Ok(())
    }

    fn publish_value(
        &mut self,
        name: &str,
        kind: PersistenceKind,
        instance: Option<&str>,
        value: ProtoValue,
    ) -> HandlerResult<()> {
        let channel_name = self.registered_name(name, kind, instance)?;
        self.publications.push(ChannelMessage {
            channel_name: channel_name.clone(),
            value: Some(value),
        });
        if self.method == InvocationMethod::Rpc {
            self.channel_infos
                .entry(channel_name)
                .and_modify(|info| info.size += 1)
                .or_insert(ChannelInfo { size: 1 });
        }
        Ok(())
    }

    fn channel_size_value(
        &self,
        name: &str,
        kind: PersistenceKind,
        instance: Option<&str>,
    ) -> HandlerResult<usize> {
        let channel_name = self.registered_name(name, kind, instance)?;
        Ok(self
            .channel_infos
            .get(&channel_name)
            .map_or(0, |info| usize::try_from(info.size).unwrap_or_default()))
    }

    fn channel_results_value<T: Value>(
        &self,
        name: &str,
        kind: PersistenceKind,
        instance: Option<&str>,
    ) -> HandlerResult<Vec<T>> {
        let channel_name = self.registered_name(name, kind, instance)?;
        let Some(results) = &self.condition_results else {
            return Ok(Vec::new());
        };
        let mut decoded = Vec::new();
        for result in &results.channel_results {
            if result.channel_name == channel_name
                && result.condition_status == ConditionStatus::Completed as i32
            {
                for value in &result.values {
                    decoded.push(value_mapper::decode_handler(value)?);
                }
            }
        }
        Ok(decoded)
    }

    fn registered_name(
        &self,
        name: &str,
        kind: PersistenceKind,
        instance: Option<&str>,
    ) -> HandlerResult<String> {
        let definition = self.flow.persistence.get(name).ok_or_else(|| {
            HandlerError::new(format!(
                "persistence definition does not belong to Flow: {name}"
            ))
        })?;
        if definition.kind != kind {
            return Err(HandlerError::new(format!(
                "persistence definition kind does not match: {name}"
            )));
        }
        match kind {
            PersistenceKind::AttributeMap | PersistenceKind::ChannelMap => instance
                .map(|instance| physical_name(name, instance))
                .ok_or_else(|| HandlerError::new(format!("{name} requires an instance"))),
            PersistenceKind::Attribute | PersistenceKind::Channel if instance.is_none() => {
                Ok(name.to_string())
            }
            _ => Err(HandlerError::new(format!(
                "static persistence definition cannot use an instance: {name}"
            ))),
        }
    }
}

#[derive(Clone)]
pub(crate) struct InvocationCancellation {
    inner: Arc<InvocationCancellationInner>,
}

struct InvocationCancellationInner {
    cancelled: AtomicBool,
    mutex: Mutex<()>,
    condition: Condvar,
}

impl InvocationCancellation {
    fn new() -> Self {
        Self {
            inner: Arc::new(InvocationCancellationInner {
                cancelled: AtomicBool::new(false),
                mutex: Mutex::new(()),
                condition: Condvar::new(),
            }),
        }
    }

    fn wait(&self) {
        let mut guard = self.inner.mutex.lock().expect("cancellation lock");
        while !self.is_cancelled() {
            guard = self.inner.condition.wait(guard).expect("cancellation lock");
        }
    }

    fn is_cancelled(&self) -> bool {
        self.inner.cancelled.load(Ordering::Acquire)
    }

    pub(crate) fn cancel(&self) {
        let _guard = self.inner.mutex.lock().expect("cancellation lock");
        self.inner.cancelled.store(true, Ordering::Release);
        self.inner.condition.notify_all();
    }
}

fn system_time(timestamp: i64) -> SystemTime {
    if timestamp >= 0 {
        SystemTime::UNIX_EPOCH + Duration::from_secs(timestamp as u64)
    } else {
        SystemTime::UNIX_EPOCH - Duration::from_secs(timestamp.unsigned_abs())
    }
}

fn map_values(kind: &str, entries: Vec<Kv>) -> HandlerResult<HashMap<String, ProtoValue>> {
    let mut mapped = HashMap::new();
    for entry in entries {
        require_name(&entry.key, kind)?;
        let value = entry
            .value
            .ok_or_else(|| HandlerError::new(format!("{kind} {} has no Value", entry.key)))?;
        if mapped.insert(entry.key.clone(), value).is_some() {
            return Err(HandlerError::new(format!("duplicate {kind} {}", entry.key)));
        }
    }
    Ok(mapped)
}

fn require_name(value: &str, kind: &str) -> HandlerResult<()> {
    if value.is_empty() {
        Err(HandlerError::new(format!("{kind} is required")))
    } else {
        Ok(())
    }
}
