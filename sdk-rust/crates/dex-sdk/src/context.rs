// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{HashMap, HashSet};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use std::time::{Duration, SystemTime};

use dex_protocol::dex::{
    AttributeWrite, ChannelInfo, ChannelMessage as ProtoChannelMessage, ChannelMessageDeletion,
    ChannelValues, ConditionResults, ConditionStatus, Context as ProtoContext, Kv, StepStreamWrite,
    Value as ProtoValue, value,
};

use crate::attribute::{
    AttributeWriteSettings, require_registered_hard_delete_setting,
    validate_hard_delete_attribute_write,
};
use crate::persistence::PersistenceKind;
use crate::registry::{RegisteredFlow, decode_instance, physical_name, validate_map_instance};
use crate::server_protocol::NegotiatedServerProtocolVersion;
use crate::value_mapper;
use crate::worker_output::StepOutputEmitter;
use crate::{
    Attribute, AttributeMap, Channel, ChannelMap, ChannelMessage, FlowResult, HandlerError,
    HandlerResult, Stream, Value,
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
    pub(crate) loaded_channel_messages: HashMap<String, ChannelValues>,
    pub(crate) loaded_attribute_map_instances: Vec<String>,
    pub(crate) loaded_channel_names: Vec<String>,
    pub(crate) loaded_channel_map_instances: Vec<String>,
    pub(crate) negotiated_protocol_version: NegotiatedServerProtocolVersion,
}

pub(crate) struct InvocationOutputs {
    pub(crate) attributes: Vec<AttributeWrite>,
    pub(crate) locals: Vec<Kv>,
    pub(crate) events: Vec<Kv>,
    pub(crate) publications: Vec<ProtoChannelMessage>,
    pub(crate) channel_deletions: Vec<ChannelMessageDeletion>,
}

/// Describes the final failure that caused a Step method recovery.
///
/// Read this value from [`Context::recovery_error`]. Worker failures preserve their original
/// error type and detail. Dex failures such as timeouts use the corresponding failure type.
///
/// ```no_run
/// # fn inspect(context: &dex_sdk::Context) {
/// if let Some(error) = context.recovery_error() {
///     eprintln!("Recovering from {}: {}", error.error_type(), error.detail());
/// }
/// # }
/// ```
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RecoveryErrorInfo {
    detail: String,
    error_type: String,
    attempt_progress: StepAttemptProgress,
}

/// Reports how far attempts of a failed Step method progressed.
///
/// Since 1.35.0.
///
/// Read [`RecoveryErrorInfo::attempt_progress`] during recovery before deciding whether to repeat
/// side effects. Progress describes all attempts of that method call, rather than just the last one.
/// A handler may have performed side effects for both [`Self::NotCompleted`] and [`Self::Unspecified`].
/// These values correspond to NOT_STARTED, NOT_COMPLETED, and UNSPECIFIED on the wire.
/// NotCompleted does not prove that an external effect failed; reconcile it or repeat it idempotently.
/// Unspecified also covers an unknown progress value and must be handled conservatively.
/// Only Sync can report NotStarted. Async failures always report NotCompleted because an
/// unpersisted start can replay even if an earlier execution already ran. A disconnect after
/// hand-out, or an input loading or decoding failure, conservatively reports NotCompleted.
/// Gracefully returning a task without running it consumes no retry and reports no failure.
///
/// ```no_run
/// # fn recover(context: &dex_sdk::Context) {
/// if let Some(error) = context.recovery_error() {
///     match error.attempt_progress() {
///         dex_sdk::StepAttemptProgress::NotStarted => { /* no handler side effects */ }
///         _ => { /* reconcile possible side effects before retrying */ }
///     }
/// }
/// # }
/// ```
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
#[non_exhaustive]
pub enum StepAttemptProgress {
    /// An older Server supplied no progress; treat this like [`Self::NotCompleted`].
    Unspecified,
    /// No handler ran in any attempt of this Step method call.
    NotStarted,
    /// At least one attempt may have run, but no attempt completed.
    NotCompleted,
}

impl StepAttemptProgress {
    fn from_protocol_value(value: i32) -> Self {
        match dex_protocol::dex::StepAttemptProgress::try_from(value) {
            Ok(dex_protocol::dex::StepAttemptProgress::NotStarted) => Self::NotStarted,
            Ok(dex_protocol::dex::StepAttemptProgress::NotCompleted) => Self::NotCompleted,
            Ok(dex_protocol::dex::StepAttemptProgress::Unspecified) | Err(_) => Self::Unspecified,
        }
    }
}

impl RecoveryErrorInfo {
    /// Returns the furthest progress across all attempts of the failed Step method call.
    ///
    /// [`StepAttemptProgress::NotStarted`] guarantees its handler did not run. Both other values
    /// mean side effects may have happened. The value is copied; see [`StepAttemptProgress`] for
    /// a recovery example. Older Servers report [`StepAttemptProgress::Unspecified`].
    /// Since 1.35.0.
    pub fn attempt_progress(&self) -> StepAttemptProgress {
        self.attempt_progress
    }

    /// Returns the failure detail supplied by the Worker or backend.
    pub fn detail(&self) -> &str {
        &self.detail
    }

    /// Returns the original Worker error type or backend failure type.
    pub fn error_type(&self) -> &str {
        &self.error_type
    }
}

/// Provides invocation metadata and staged durable changes to Step and RPC handlers.
///
/// A Context belongs to one handler attempt and must not outlive the call. Attribute writes,
/// Channel publications, locals, and events become visible atomically only when the handler returns
/// successfully. Application values are freshly decoded and owned by the invocation.
/// Heartbeats and Stream messages are emitted during the call, independently of the final result.
///
/// The Rust Context exposes cancellation, not an absolute attempt-deadline accessor. Bound external
/// calls with their own timeout and check [`Self::is_cancelled`] between bounded units of work.
/// The Worker observes the server-supplied attempt deadline and requests cancellation on expiry.
/// There is no separate attempt ID accessor: use Flow ID, run ID, Step execution ID, and attempt
/// number together for diagnostics. These identifiers do not make external effects idempotent.
pub struct Context {
    method: InvocationMethod,
    flow: RegisteredFlow,
    output_emitter: Option<StepOutputEmitter>,
    metadata: ProtoContext,
    recovery_error: Option<RecoveryErrorInfo>,
    attributes: HashMap<String, ProtoValue>,
    locals: HashMap<String, ProtoValue>,
    condition_results: Option<ConditionResults>,
    channel_infos: HashMap<String, ChannelInfo>,
    loaded_channel_messages: HashMap<String, ChannelValues>,
    loaded_attribute_map_instances: HashSet<String>,
    loaded_channel_names: HashSet<String>,
    loaded_channel_map_instances: HashSet<String>,
    attribute_writes: HashMap<String, AttributeWrite>,
    local_writes: HashMap<String, Kv>,
    events: Vec<Kv>,
    event_names: HashSet<String>,
    publications: Vec<ProtoChannelMessage>,
    channel_deletions: Vec<ChannelMessageDeletion>,
    cancellation: InvocationCancellation,
    runtime: Option<tokio::runtime::Handle>,
    step_output_finalizers: Vec<Arc<dyn StepOutputFinalizer>>,
    step_outputs_finalized: bool,
    negotiated_protocol_version: NegotiatedServerProtocolVersion,
}

pub(crate) trait StepOutputFinalizer: Send + Sync {
    fn finalize_step_output(&self) -> HandlerResult<()>;
}

impl Context {
    pub(crate) fn new(
        input: ContextInput,
        output_emitter: Option<StepOutputEmitter>,
        cancellation: InvocationCancellation,
    ) -> HandlerResult<Self> {
        let recovery_error =
            input
                .metadata
                .recovery_error
                .as_ref()
                .map(|error| RecoveryErrorInfo {
                    attempt_progress: StepAttemptProgress::from_protocol_value(
                        error.attempt_progress,
                    ),
                    detail: error.detail.clone(),
                    error_type: error.error_type.clone(),
                });
        Ok(Self {
            method: input.method,
            flow: input.flow,
            output_emitter,
            metadata: input.metadata,
            recovery_error,
            attributes: map_values("Attribute", input.attributes)?,
            locals: map_values("step-execution local", input.locals)?,
            condition_results: input.condition_results,
            channel_infos: input.channel_infos,
            loaded_channel_messages: input.loaded_channel_messages,
            loaded_attribute_map_instances: input
                .loaded_attribute_map_instances
                .into_iter()
                .collect(),
            loaded_channel_names: input.loaded_channel_names.into_iter().collect(),
            loaded_channel_map_instances: input.loaded_channel_map_instances.into_iter().collect(),
            attribute_writes: HashMap::new(),
            local_writes: HashMap::new(),
            events: Vec::new(),
            event_names: HashSet::new(),
            publications: Vec::new(),
            channel_deletions: Vec::new(),
            cancellation,
            runtime: tokio::runtime::Handle::try_current().ok(),
            step_output_finalizers: Vec::new(),
            step_outputs_finalized: false,
            negotiated_protocol_version: input.negotiated_protocol_version,
        })
    }

    /// Returns the application-assigned Flow ID.
    pub fn flow_id(&self) -> &str {
        &self.metadata.flow_id
    }

    /// Returns the server-assigned run ID for this execution.
    ///
    /// A Flow ID identifies the application operation; a run ID identifies one server execution.
    /// Do not assume a run ID remains fixed for the entire lifetime of a Flow ID.
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

    /// Returns the final failure that caused recovery, or `None` outside a recovery Step.
    pub fn recovery_error(&self) -> Option<&RecoveryErrorInfo> {
        self.recovery_error.as_ref()
    }

    /// Returns when the first attempt of this handler invocation started.
    pub fn first_attempt_at(&self) -> SystemTime {
        system_time(self.metadata.first_attempt_timestamp)
    }

    /// Returns the one-based handler attempt number.
    ///
    /// With 1.35.0 (Server protocol 4): an unpersisted Async attempt start can replay with the same
    /// number.
    /// Attempts count within one method call; they are not unique external-effect identifiers.
    pub fn attempt(&self) -> u32 {
        u32::try_from(self.metadata.attempt).unwrap_or_default()
    }

    /// Records a heartbeat without a Value and clears persisted heartbeat details.
    ///
    /// The call blocks only while the Worker's one-frame output buffer is full. It does not wait
    /// for Dex to process the heartbeat. Flow RPC Contexts reject this operation.
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] for an RPC Context, cancellation, or a closed Worker response
    /// stream.
    pub fn record_heartbeat(&mut self) -> HandlerResult<()> {
        self.step_output_emitter()?.send_heartbeat(None)
    }

    /// Records a heartbeat carrying a typed checkpoint Value.
    ///
    /// A later attempt can decode the most recently accepted checkpoint with
    /// [`Self::last_heartbeat_value`]. Both durability modes preserve checkpoint values.
    /// The call uses the same bounded local buffering as [`Self::record_heartbeat`].
    ///
    /// # Examples
    ///
    /// ```
    /// use dex_sdk::{Context, HandlerResult};
    /// use serde::{Deserialize, Serialize};
    ///
    /// #[derive(Deserialize, Serialize)]
    /// struct Checkpoint {
    ///     offset: u64,
    /// }
    ///
    /// fn import_batch(context: &mut Context, next_offset: u64) -> HandlerResult<()> {
    ///     let previous = context.last_heartbeat_value::<Checkpoint>()?;
    ///     let offset = previous.map_or(next_offset, |checkpoint| checkpoint.offset);
    ///     context.record_heartbeat_value(Checkpoint { offset })
    /// }
    /// ```
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] for an RPC Context, encoding failure, cancellation, or a closed
    /// Worker response stream.
    pub fn record_heartbeat_value<T: Value>(&mut self, value: T) -> HandlerResult<()> {
        let value = value_mapper::encode_handler(&value)?;
        self.step_output_emitter()?.send_heartbeat(Some(value))
    }

    /// Decodes the checkpoint persisted by the previous regular attempt.
    ///
    /// `None` means no heartbeat Value is present. To distinguish a null Value, request an
    /// optional application type: `last_heartbeat_value::<Option<T>>()` returns `Some(None)` for a
    /// present null Value.
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] for an RPC Context or when the persisted Value cannot be decoded
    /// as `T`.
    pub fn last_heartbeat_value<T: Value>(&self) -> HandlerResult<Option<T>> {
        if self.method == InvocationMethod::Rpc {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                "Heartbeat values require a Step Context",
            ));
        }
        self.metadata
            .last_heartbeat_value
            .as_ref()
            .map(value_mapper::decode_handler)
            .transpose()
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
            attribute.write_settings(),
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
            attribute.write_settings(),
        )
    }

    /// Stages deletion of an Attribute value.
    pub fn delete_attribute<T>(&mut self, attribute: &Attribute<T>) -> HandlerResult<()> {
        self.set_attribute_value(
            attribute.name(),
            PersistenceKind::Attribute,
            None,
            value_mapper::deletion(),
            attribute.write_settings(),
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
            attribute.write_settings(),
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
        if !self
            .loaded_attribute_map_instances
            .contains(&format!("{}/", attribute.name()))
        {
            return Err(attribute_map_not_loaded(format!(
                "all AttributeMap instances were not loaded for this invocation: {}",
                attribute.name()
            )));
        }
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

    /// Returns one singleton Channel's loaded pending-message snapshot.
    pub fn pending_channel_messages<T: Value>(
        &self,
        channel: &Channel<T>,
    ) -> HandlerResult<Vec<ChannelMessage<T>>> {
        self.pending_channel_messages_value(channel.name(), PersistenceKind::Channel, None)
    }

    /// Returns one Channel-map instance's loaded pending-message snapshot.
    pub fn pending_channel_map_messages<T: Value>(
        &self,
        channel: &ChannelMap<T>,
        instance: &str,
    ) -> HandlerResult<Vec<ChannelMessage<T>>> {
        self.pending_channel_messages_value(
            channel.name(),
            PersistenceKind::ChannelMap,
            Some(instance),
        )
    }

    pub(crate) fn channel_map_keys<T>(
        &self,
        channel: &ChannelMap<T>,
    ) -> HandlerResult<Vec<String>> {
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

    /// Appends one best-effort Stream message to the current Worker response stream.
    ///
    /// The call returns after the Worker's bounded output buffer accepts the frame. It does not
    /// wait for Dex or the Stream Store, and a later server-side write failure is not returned to
    /// the handler. One invocation may append any number of messages to the same Stream.
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] for an RPC or Flow timeout Context, an unregistered Stream, a
    /// capacity mismatch, encoding failure, cancellation, or a closed Worker response stream.
    pub fn write_stream<T: Value>(&mut self, stream: &Stream<T>, value: T) -> HandlerResult<()> {
        let output_emitter = self.step_output_emitter()?.clone();
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
        output_emitter.send_stream_write(StepStreamWrite {
            stream_name: stream.name().to_string(),
            stream_capacity_bytes: stream.stream_capacity_bytes(),
            value: Some(value_mapper::encode_handler(&value)?),
        })
    }

    pub(crate) fn prepare_buffered_text_stream(
        &self,
        stream: &Stream<String>,
    ) -> HandlerResult<(
        StepOutputEmitter,
        InvocationCancellation,
        tokio::runtime::Handle,
    )> {
        let output_emitter = self.step_output_emitter()?.clone();
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
        let runtime = self.runtime.clone().ok_or_else(|| {
            HandlerError::new(
                "dex_sdk::HandlerError",
                "Buffered Stream runtime is unavailable",
            )
        })?;
        Ok((output_emitter, self.cancellation.clone(), runtime))
    }

    pub(crate) fn register_step_output_finalizer(
        &mut self,
        finalizer: Arc<dyn StepOutputFinalizer>,
    ) -> HandlerResult<()> {
        if self.step_outputs_finalized {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                "Buffered Stream invocation has finished",
            ));
        }
        self.step_output_finalizers.push(finalizer);
        Ok(())
    }

    pub(crate) fn finalize_step_outputs<T>(
        &mut self,
        result: HandlerResult<T>,
    ) -> HandlerResult<T> {
        if self.step_outputs_finalized {
            return result;
        }
        self.step_outputs_finalized = true;
        let mut result = result;
        for finalizer in &self.step_output_finalizers {
            if let Err(finalization_failure) = finalizer.finalize_step_output() {
                match &mut result {
                    Ok(_) => result = Err(finalization_failure),
                    Err(handler_failure) => {
                        handler_failure.attach_finalizer_error(&finalization_failure);
                    }
                }
            }
        }
        result
    }

    pub(crate) fn cancellation(&self) -> InvocationCancellation {
        self.cancellation.clone()
    }

    pub(crate) fn take_outputs(self) -> InvocationOutputs {
        InvocationOutputs {
            attributes: self.attribute_writes.into_values().collect(),
            locals: self.local_writes.into_values().collect(),
            events: self.events,
            publications: self.publications,
            channel_deletions: self.channel_deletions,
        }
    }

    fn step_output_emitter(&self) -> HandlerResult<&StepOutputEmitter> {
        if self.method == InvocationMethod::Rpc {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                "Step progress requires a Step Context",
            ));
        }
        if self.cancellation.is_cancelled() {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                "Step invocation is canceled",
            ));
        }
        self.output_emitter.as_ref().ok_or_else(|| {
            HandlerError::new("dex_sdk::HandlerError", "Step output stream is unavailable")
        })
    }

    fn get_attribute_value<T: Value>(
        &self,
        name: &str,
        kind: PersistenceKind,
        instance: Option<&str>,
    ) -> HandlerResult<Option<T>> {
        let key = self.registered_name(name, kind, instance)?;
        if kind == PersistenceKind::AttributeMap {
            let is_loaded = self
                .loaded_attribute_map_instances
                .contains(&format!("{name}/"))
                || self.loaded_attribute_map_instances.contains(&key);
            if !is_loaded {
                return Err(attribute_map_not_loaded(format!(
                    "AttributeMap instance was not loaded for this invocation: {key}"
                )));
            }
        }
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
        write_settings: AttributeWriteSettings,
    ) -> HandlerResult<()> {
        let key = self.registered_name(name, kind, instance)?;
        let is_registered_hard_delete_on_null = self
            .flow
            .persistence
            .get(name)
            .is_some_and(|definition| definition.is_hard_delete_on_null);
        require_registered_hard_delete_setting(
            name,
            is_registered_hard_delete_on_null,
            write_settings.is_hard_delete_on_null,
        )
        .map_err(|error| HandlerError::new("dex_sdk::HandlerError", error))?;
        let write = write_settings.attribute_write(key.clone(), value);
        validate_hard_delete_attribute_write(&write)
            .map_err(|error| HandlerError::new("dex_sdk::HandlerError", error))?;
        if write.is_hard_delete_on_null {
            self.negotiated_protocol_version
                .require_hard_delete_attribute_support(&key)
                .map_err(|error| HandlerError::new("dex_sdk::HandlerError", error))?;
        }
        self.attribute_writes.insert(key, write);
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
        self.publications.push(ProtoChannelMessage {
            channel_name: channel_name.clone(),
            value: Some(value),
            message_id: String::new(),
        });
        self.channel_infos
            .entry(channel_name)
            .and_modify(|info| info.size += 1)
            .or_insert(ChannelInfo { size: 1 });
        Ok(())
    }

    /// Stages best-effort deletion of one singleton Channel message.
    pub fn delete_channel_message<T>(
        &mut self,
        channel: &Channel<T>,
        message_id: &str,
    ) -> HandlerResult<()> {
        self.delete_channel_message_value(
            channel.name(),
            PersistenceKind::Channel,
            None,
            message_id,
        )
    }

    /// Stages best-effort deletion of one Channel-map message.
    pub fn delete_channel_map_message<T>(
        &mut self,
        channel: &ChannelMap<T>,
        instance: &str,
        message_id: &str,
    ) -> HandlerResult<()> {
        self.delete_channel_message_value(
            channel.name(),
            PersistenceKind::ChannelMap,
            Some(instance),
            message_id,
        )
    }

    fn delete_channel_message_value(
        &mut self,
        name: &str,
        kind: PersistenceKind,
        instance: Option<&str>,
        message_id: &str,
    ) -> HandlerResult<()> {
        if message_id.is_empty() {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                "Channel message ID must not be empty",
            ));
        }
        let channel_name = self.registered_name(name, kind, instance)?;
        self.channel_deletions.push(ChannelMessageDeletion {
            channel_name: channel_name.clone(),
            message_id: message_id.to_string(),
        });
        if let Some(info) = self.channel_infos.get_mut(&channel_name) {
            info.size = info.size.saturating_sub(1);
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

    fn pending_channel_messages_value<T: Value>(
        &self,
        name: &str,
        kind: PersistenceKind,
        instance: Option<&str>,
    ) -> HandlerResult<Vec<ChannelMessage<T>>> {
        let physical_name = self.registered_name(name, kind, instance)?;
        let is_loaded = match kind {
            PersistenceKind::Channel => self.loaded_channel_names.contains(&physical_name),
            PersistenceKind::ChannelMap => {
                self.loaded_channel_map_instances
                    .contains(&format!("{name}/"))
                    || self.loaded_channel_map_instances.contains(&physical_name)
            }
            _ => false,
        };
        if !is_loaded {
            return Err(channel_messages_not_loaded(format!(
                "Channel messages were not loaded for this invocation: {physical_name}"
            )));
        }
        self.loaded_channel_messages
            .get(&physical_name)
            .map_or(&[][..], |values| values.messages.as_slice())
            .iter()
            .map(|message| {
                let value = message.value.as_ref().ok_or_else(|| {
                    HandlerError::new(
                        "dex_sdk::HandlerError",
                        format!("Channel message {} has no Value", message.message_id),
                    )
                })?;
                Ok(ChannelMessage {
                    message_id: message.message_id.clone(),
                    value: value_mapper::decode_handler(value)?,
                })
            })
            .collect()
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
            PersistenceKind::AttributeMap | PersistenceKind::ChannelMap => {
                let instance = instance.ok_or_else(|| {
                    HandlerError::new(
                        "dex_sdk::HandlerError",
                        format!("{name} requires an instance"),
                    )
                })?;
                validate_map_instance(instance)
                    .map_err(|error| HandlerError::new("dex_sdk::HandlerError", error))?;
                Ok(physical_name(name, instance))
            }
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

fn attribute_map_not_loaded(message: impl Into<String>) -> HandlerError {
    HandlerError::new("dex_sdk::AttributeMapNotLoadedError", message)
}

fn channel_messages_not_loaded(message: impl Into<String>) -> HandlerError {
    HandlerError::new("dex_sdk::ChannelMessagesNotLoadedError", message)
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
    notification: tokio::sync::Notify,
}

impl InvocationCancellation {
    pub(crate) fn new() -> Self {
        Self {
            inner: Arc::new(InvocationCancellationInner {
                cancelled: AtomicBool::new(false),
                mutex: Mutex::new(()),
                condition: Condvar::new(),
                notification: tokio::sync::Notify::new(),
            }),
        }
    }

    fn wait(&self) {
        let mut guard = self.inner.mutex.lock().expect("cancellation lock");
        while !self.is_cancelled() {
            guard = self.inner.condition.wait(guard).expect("cancellation lock");
        }
    }

    pub(crate) fn is_cancelled(&self) -> bool {
        self.inner.cancelled.load(Ordering::Acquire)
    }

    pub(crate) fn cancel(&self) {
        let _guard = self.inner.mutex.lock().expect("cancellation lock");
        self.inner.cancelled.store(true, Ordering::Release);
        self.inner.condition.notify_all();
        self.inner.notification.notify_waiters();
    }

    pub(crate) async fn cancelled(&self) {
        let notified = self.inner.notification.notified();
        if self.is_cancelled() {
            return;
        }
        notified.await;
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
#[path = "context_tests.rs"]
mod tests;
