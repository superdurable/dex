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
    Context as ProtoContext, Kv, Value as ProtoValue, WriteStreamRequest,
    flow_service_client::FlowServiceClient, value,
};
use tokio::runtime::Handle;
use tonic::transport::Channel as TransportChannel;

use crate::persistence::PersistenceKind;
use crate::registry::{RegisteredFlow, decode_instance, physical_name};
use crate::value_mapper;
use crate::{
    Attribute, AttributeMap, Channel, ChannelMap, FlowResult, HandlerError, HandlerResult, Stream,
    Value,
};

#[derive(Clone, Copy, Eq, PartialEq)]
pub(crate) enum InvocationMethod {
    WaitFor,
    Execute,
    Rpc,
}

pub(crate) struct ContextInput {
    pub(crate) method: InvocationMethod,
    pub(crate) flow: RegisteredFlow,
    pub(crate) metadata: ProtoContext,
    pub(crate) attributes: Vec<Kv>,
    pub(crate) locals: Vec<Kv>,
    pub(crate) condition_results: Option<ConditionResults>,
    pub(crate) channel_infos: HashMap<String, ChannelInfo>,
}

/// Provides invocation metadata and staged durable changes to Step and RPC handlers.
///
/// A Context belongs to one handler attempt and must not outlive the call. Attribute writes,
/// Channel publications, locals, and events become visible atomically only when the handler returns
/// successfully. Application values are freshly decoded and owned by the invocation.
pub struct Context {
    method: InvocationMethod,
    flow: RegisteredFlow,
    runtime_handle: Handle,
    flow_service: FlowServiceClient<TransportChannel>,
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
    stream_writes: HashSet<usize>,
    cancellation: InvocationCancellation,
}

impl Context {
    pub(crate) fn new(
        input: ContextInput,
        runtime_handle: Handle,
        flow_service: FlowServiceClient<TransportChannel>,
    ) -> HandlerResult<Self> {
        Ok(Self {
            method: input.method,
            flow: input.flow,
            runtime_handle,
            flow_service,
            metadata: input.metadata,
            attributes: map_values("Attribute", input.attributes)?,
            locals: map_values("step-execution local", input.locals)?,
            condition_results: input.condition_results,
            channel_infos: input.channel_infos,
            attribute_writes: HashMap::new(),
            local_writes: HashMap::new(),
            events: Vec::new(),
            event_names: HashSet::new(),
            publications: Vec::new(),
            stream_writes: HashSet::new(),
            cancellation: InvocationCancellation::new(),
        })
    }

    /// Returns the application-assigned Flow ID.
    pub fn flow_id(&self) -> &str {
        &self.metadata.flow_id
    }

    /// Returns the server-assigned run ID for this execution.
    pub fn run_id(&self) -> &str {
        &self.metadata.run_id
    }

    /// Returns when the current Flow run started.
    pub fn flow_started_at(&self) -> SystemTime {
        system_time(self.metadata.flow_started_timestamp)
    }

    /// Returns the current Step execution ID, or an empty string for an RPC.
    pub fn step_execution_id(&self) -> &str {
        &self.metadata.step_execution_id
    }

    /// Returns the predecessor Step execution ID, or an empty string when absent.
    pub fn from_step_execution_id(&self) -> &str {
        &self.metadata.from_step_execution_id
    }

    /// Returns when the first attempt of this handler invocation started.
    pub fn first_attempt_at(&self) -> SystemTime {
        system_time(self.metadata.first_attempt_timestamp)
    }

    /// Returns the one-based handler attempt number.
    pub fn attempt(&self) -> u32 {
        u32::try_from(self.metadata.attempt).unwrap_or_default()
    }

    /// Returns whether any timer condition completed for this `execute` invocation.
    pub fn has_any_timer_fired(&self) -> bool {
        self.condition_results.as_ref().is_some_and(|results| {
            results
                .timer_results
                .iter()
                .any(|timer| timer.condition_status == ConditionStatus::Completed as i32)
        })
    }

    /// Returns whether the zero-based timer at `index` completed.
    ///
    /// Missing indexes return `false`.
    pub fn has_timer_fired(&self, index: usize) -> bool {
        self.condition_results
            .as_ref()
            .and_then(|results| results.timer_results.get(index))
            .is_some_and(|timer| timer.condition_status == ConditionStatus::Completed as i32)
    }

    /// Returns whether `execute` is running because `wait_for` exhausted retries with Proceed.
    pub fn wait_for_method_failed(&self) -> bool {
        self.condition_results
            .as_ref()
            .is_some_and(|results| results.wait_for_failed)
    }

    pub(crate) fn sub_flow_result(&self, index: usize) -> HandlerResult<FlowResult> {
        if self.method != InvocationMethod::Execute {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                "SubFlow results are available only during execute",
            ));
        }
        let result = self
            .condition_results
            .as_ref()
            .and_then(|results| results.sub_flow_results.get(index))
            .ok_or_else(|| {
                HandlerError::new(
                    "dex_sdk::HandlerError",
                    format!("SubFlow result index is unavailable: {index}"),
                )
            })?;
        FlowResult::from_proto(result).map_err(HandlerError::from_error)
    }

    pub(crate) fn sub_flow_id(&self, index: usize) -> HandlerResult<String> {
        self.sub_flow_result(index)?;
        Ok(format!(
            "SubFlow:{}-{}-{index}",
            self.flow_id(),
            self.step_execution_id()
        ))
    }

    /// Blocks the current thread until Dex cancels this handler attempt.
    ///
    /// Use this only for interruptible application work; ordinary handlers should return normally.
    pub fn wait_for_cancellation(&self) {
        self.cancellation.wait();
    }

    /// Returns immediately with the current handler-cancellation state.
    pub fn is_cancelled(&self) -> bool {
        self.cancellation.is_cancelled()
    }

    /// Stages a Step-execution-local value visible to later attempts of the same execution.
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] for an empty key or an encoding failure.
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

    /// Reads a required Step-execution-local value, including writes staged by this attempt.
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] for an empty key, a missing value, or a decoding failure.
    pub fn step_execution_local<T: Value>(&self, key: &str) -> HandlerResult<T> {
        require_name(key, "step-execution local key")?;
        let value = self
            .local_writes
            .get(key)
            .and_then(|entry| entry.value.as_ref())
            .or_else(|| self.locals.get(key))
            .ok_or_else(|| {
                HandlerError::new(
                    "dex_sdk::HandlerError",
                    format!("step-execution local {key} is missing"),
                )
            })?;
        value_mapper::decode_handler(value)
    }

    /// Records one named diagnostic event with a typed payload.
    ///
    /// Event names must be unique within the invocation.
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] for an empty or duplicate name or an encoding failure.
    pub fn record_event<T: Value>(&mut self, name: &str, value: T) -> HandlerResult<()> {
        require_name(name, "event name")?;
        if !self.event_names.insert(name.to_string()) {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                format!("event was already recorded: {name}"),
            ));
        }
        self.events.push(Kv {
            key: name.to_string(),
            value: Some(value_mapper::encode_handler(&value)?),
        });
        Ok(())
    }

    /// Reads an Attribute value, including writes staged by this invocation.
    pub fn get_attribute<T: Value>(&self, attribute: &Attribute<T>) -> HandlerResult<Option<T>> {
        self.get_attribute_value(attribute.name(), PersistenceKind::Attribute, None)
    }

    /// Reads one Attribute-map instance, including staged writes.
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

    /// Stages a typed Attribute write.
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
            attribute.sync_config(),
        )
    }

    /// Stages a typed write for one Attribute-map instance.
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
            attribute.sync_config(),
        )
    }

    /// Stages deletion of an Attribute value.
    pub fn delete_attribute<T>(&mut self, attribute: &Attribute<T>) -> HandlerResult<()> {
        self.set_attribute_value(
            attribute.name(),
            PersistenceKind::Attribute,
            None,
            value_mapper::deletion(),
            attribute.index().map(|index| index.proto_config(false)),
            attribute.sync_config(),
        )
    }

    /// Stages deletion of one Attribute-map instance.
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
            attribute.sync_config(),
        )
    }

    pub(crate) fn attribute_map_keys<T>(
        &self,
        attribute: &AttributeMap<T>,
    ) -> HandlerResult<Vec<String>> {
        self.registered_name(
            attribute.name(),
            PersistenceKind::AttributeMap,
            Some("key-check"),
        )?;
        let prefix = format!("{}/", attribute.name());
        let mut physical_keys = self
            .attributes
            .keys()
            .filter(|key| key.starts_with(&prefix))
            .cloned()
            .collect::<HashSet<_>>();
        for (key, write) in &self.attribute_writes {
            if !key.starts_with(&prefix) {
                continue;
            }
            if matches!(
                write.value.as_ref().and_then(|value| value.kind.as_ref()),
                Some(value::Kind::NullValue(_))
            ) {
                physical_keys.remove(key);
            } else {
                physical_keys.insert(key.clone());
            }
        }
        sorted_instance_keys(&prefix, physical_keys)
    }

    /// Stages one typed Channel publication.
    pub fn publish<T: Value>(&mut self, channel: &Channel<T>, value: T) -> HandlerResult<()> {
        self.publish_value(
            channel.name(),
            PersistenceKind::Channel,
            None,
            value_mapper::encode_handler(&value)?,
        )
    }

    /// Stages one typed publication to a Channel-map instance.
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

    /// Returns the current invocation snapshot's Channel queue size.
    pub fn channel_size<T>(&self, channel: &Channel<T>) -> HandlerResult<usize> {
        self.channel_size_value(channel.name(), PersistenceKind::Channel, None)
    }

    /// Returns one Channel-map instance's queue size.
    pub fn channel_map_size<T>(
        &self,
        channel: &ChannelMap<T>,
        instance: &str,
    ) -> HandlerResult<usize> {
        self.channel_size_value(channel.name(), PersistenceKind::ChannelMap, Some(instance))
    }

    pub(crate) fn channel_map_keys<T>(
        &self,
        channel: &ChannelMap<T>,
    ) -> HandlerResult<Vec<String>> {
        if self.method != InvocationMethod::Rpc {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                "ChannelMap introspection requires an RPC invocation",
            ));
        }
        self.registered_name(
            channel.name(),
            PersistenceKind::ChannelMap,
            Some("key-check"),
        )?;
        let prefix = format!("{}/", channel.name());
        let physical_keys = self
            .channel_infos
            .iter()
            .filter(|(key, info)| key.starts_with(&prefix) && info.size > 0)
            .map(|(key, _)| key.clone())
            .collect::<HashSet<_>>();
        sorted_instance_keys(&prefix, physical_keys)
    }

    /// Decodes the messages consumed by a satisfied Channel condition.
    pub fn channel_results<T: Value>(&self, channel: &Channel<T>) -> HandlerResult<Vec<T>> {
        self.channel_results_value(channel.name(), PersistenceKind::Channel, None)
    }

    /// Decodes messages consumed by one satisfied Channel-map condition.
    pub fn channel_map_results<T: Value>(
        &self,
        channel: &ChannelMap<T>,
        instance: &str,
    ) -> HandlerResult<Vec<T>> {
        self.channel_results_value(channel.name(), PersistenceKind::ChannelMap, Some(instance))
    }

    /// Appends one immediate best-effort Stream message from a Step invocation.
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] for RPC Contexts, unregistered or duplicate Streams, encoding
    /// failures, or a failed FlowService write.
    pub fn write_stream<T: Value>(&mut self, stream: &Stream<T>, value: T) -> HandlerResult<()> {
        if self.method == InvocationMethod::Rpc {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                "Stream writes require a Step Context",
            ));
        }
        let definition = self
            .flow
            .persistence
            .get(stream.name())
            .filter(|definition| {
                definition.kind == PersistenceKind::Stream
                    && definition.stream_identity == Some(stream.identity())
            })
            .ok_or_else(|| {
                HandlerError::new(
                    "dex_sdk::HandlerError",
                    format!("Stream does not belong to Flow: {}", stream.name()),
                )
            })?;
        if definition.stream_capacity_bytes != Some(stream.stream_capacity_bytes()) {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                format!(
                    "Stream capacity does not match its registered definition: {}",
                    stream.name()
                ),
            ));
        }
        if self.stream_writes.contains(&stream.identity()) {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                format!(
                    "Stream {} was already written by this Step execution",
                    stream.name()
                ),
            ));
        }
        let request = WriteStreamRequest {
            flow_id: self.flow_id().to_string(),
            flow_type: self.flow.name.to_string(),
            stream_name: stream.name().to_string(),
            max_estimated_bytes: stream.stream_capacity_bytes(),
            value: Some(value_mapper::encode_handler(&value)?),
            idempotency_key: format!("{}#{}", self.run_id(), self.step_execution_id()),
        };
        let mut service = self.flow_service.clone();
        self.runtime_handle
            .block_on(async move { service.write_stream(request).await })
            .map_err(|status| {
                HandlerError::new("tonic::Status", format!("WriteStream failed: {status}"))
            })?;
        self.stream_writes.insert(stream.identity());
        Ok(())
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
            let value = write.value.as_ref().ok_or_else(|| {
                HandlerError::new(
                    "dex_sdk::HandlerError",
                    format!("Attribute write {key} has no Value"),
                )
            })?;
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
        sync_config: Option<dex_protocol::dex::AttributeSyncConfig>,
    ) -> HandlerResult<()> {
        let key = self.registered_name(name, kind, instance)?;
        self.attribute_writes.insert(
            key.clone(),
            AttributeWrite {
                key,
                value: Some(value),
                index_config,
                sync_config,
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
            HandlerError::new(
                "dex_sdk::HandlerError",
                format!("persistence definition does not belong to Flow: {name}"),
            )
        })?;
        if definition.kind != kind {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                format!("persistence definition kind does not match: {name}"),
            ));
        }
        match kind {
            PersistenceKind::AttributeMap | PersistenceKind::ChannelMap => instance
                .map(|instance| physical_name(name, instance))
                .ok_or_else(|| {
                    HandlerError::new(
                        "dex_sdk::HandlerError",
                        format!("{name} requires an instance"),
                    )
                }),
            PersistenceKind::Attribute | PersistenceKind::Channel if instance.is_none() => {
                Ok(name.to_string())
            }
            _ => Err(HandlerError::new(
                "dex_sdk::HandlerError",
                format!("static persistence definition cannot use an instance: {name}"),
            )),
        }
    }
}

fn sorted_instance_keys(
    prefix: &str,
    physical_keys: HashSet<String>,
) -> HandlerResult<Vec<String>> {
    let mut keys = physical_keys
        .iter()
        .map(|key| {
            decode_instance(key, prefix)
                .map_err(|error| HandlerError::new("dex_sdk::HandlerError", error))
        })
        .collect::<HandlerResult<Vec<_>>>()?;
    keys.sort();
    Ok(keys)
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
        let value = entry.value.ok_or_else(|| {
            HandlerError::new(
                "dex_sdk::HandlerError",
                format!("{kind} {} has no Value", entry.key),
            )
        })?;
        if mapped.insert(entry.key.clone(), value).is_some() {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                format!("duplicate {kind} {}", entry.key),
            ));
        }
    }
    Ok(mapped)
}

fn require_name(value: &str, kind: &str) -> HandlerResult<()> {
    if value.is_empty() {
        Err(HandlerError::new(
            "dex_sdk::HandlerError",
            format!("{kind} is required"),
        ))
    } else {
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::{Context, ContextInput, InvocationMethod};
    use crate::{
        AttributeMap, ChannelMap, Flow, PersistenceSchema, Registry, registry::physical_name,
        value_mapper,
    };
    use dex_protocol::dex::{
        ChannelInfo, Context as ProtoContext, Kv, flow_service_client::FlowServiceClient,
    };
    use std::collections::HashMap;
    use tokio::runtime::Runtime;
    use tonic::transport::Endpoint;

    struct MapFlow {
        attributes: AttributeMap<String>,
        channels: ChannelMap<String>,
    }

    impl Flow for MapFlow {
        type StartInput = ();

        fn persistence(&self) -> PersistenceSchema {
            PersistenceSchema::new()
                .attribute_map(&self.attributes)
                .channel_map(&self.channels)
        }
    }

    #[test]
    fn map_introspection_tracks_buffered_changes() {
        let flow = MapFlow {
            attributes: AttributeMap::new("items"),
            channels: ChannelMap::new("messages"),
        };
        let registry = Registry::new().register(flow).expect("register map Flow");
        let registered = registry.flow("MapFlow").expect("lookup map Flow").clone();
        let attributes = AttributeMap::<String>::new("items");
        let channels = ChannelMap::<String>::new("messages");
        let special = "special / key";
        let runtime = Runtime::new().expect("create test runtime");
        let flow_service = {
            let _runtime_guard = runtime.enter();
            FlowServiceClient::new(Endpoint::from_static("http://127.0.0.1:1").connect_lazy())
        };
        let mut context = Context::new(
            ContextInput {
                method: InvocationMethod::Rpc,
                flow: registered,
                metadata: ProtoContext::default(),
                attributes: vec![
                    Kv {
                        key: physical_name("items", special),
                        value: Some(value_mapper::encode_handler(&"initial".to_string()).unwrap()),
                    },
                    Kv {
                        key: physical_name("items", "z"),
                        value: Some(value_mapper::encode_handler(&"remove".to_string()).unwrap()),
                    },
                ],
                locals: Vec::new(),
                condition_results: None,
                channel_infos: HashMap::from([
                    (physical_name("messages", special), ChannelInfo { size: 1 }),
                    (physical_name("messages", "empty"), ChannelInfo { size: 0 }),
                ]),
            },
            runtime.handle().clone(),
            flow_service,
        )
        .expect("create RPC Context");

        assert_eq!(
            vec![special.to_string(), "z".to_string()],
            attributes.all_instance_keys(&context).unwrap()
        );
        attributes
            .set(&mut context, "a", "added".to_string())
            .unwrap();
        attributes.delete(&mut context, "z").unwrap();
        assert_eq!(
            vec!["a".to_string(), special.to_string()],
            attributes.all_instance_keys(&context).unwrap()
        );
        assert_eq!(2, attributes.map_size(&context).unwrap());

        assert_eq!(
            vec![special.to_string()],
            channels.all_instance_keys(&context).unwrap()
        );
        channels
            .publish(&mut context, "a", "published".to_string())
            .unwrap();
        assert_eq!(
            vec!["a".to_string(), special.to_string()],
            channels.all_instance_keys(&context).unwrap()
        );
        assert_eq!(2, channels.map_size(&context).unwrap());
    }
}
