// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{HashMap, HashSet};
use std::error::Error;
use std::fmt::{Display, Formatter};

use crate::context::Context;
use crate::persistence::{PersistenceDefinition, PersistenceKind};
use crate::rpc::RegisteredRpc;
use crate::step_options::ErasedStepOptions;
use crate::{AttributeLock, HandlerError, HandlerResult, SdkError, SdkResult};

const MISSING_ATTRIBUTE_LOCK_ERROR_TYPE: &str = "dex_sdk::MissingAttributeLockError";

/// Names the lock that every write or publish of one definition requires.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) enum LockRequirement {
    /// Requires the lock of the written Attribute or AttributeMap instance.
    OwnKey,
    /// Requires the lock of the named singleton Attribute.
    Attribute(String),
}

/// Records one definition's requirement in a [`crate::PersistenceSchema`].
#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct DeclaredLockRequirement {
    pub(crate) definition_name: String,
    pub(crate) kind: PersistenceKind,
    pub(crate) requirement: LockRequirement,
}

/// Identifies one buffered Attribute write or Channel publish by definition name and instance.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct BufferedAccessTarget {
    pub(crate) name: String,
    pub(crate) instance: Option<String>,
    pub(crate) physical_name: String,
}

/// Holds a Flow's declared requirements and the registration-time locks of its Steps.
#[derive(Clone, Debug, Default)]
pub(crate) struct RegisteredLockRequirements {
    requirements: HashMap<String, LockRequirement>,
    step_lock_keys: HashMap<&'static str, StepLockKeys>,
}

#[derive(Clone, Debug, Default)]
struct StepLockKeys {
    wait_for: HashSet<String>,
    execute: HashSet<String>,
}

/// Selects the handler invocation whose registration-time locks a check compares.
#[derive(Clone, Copy)]
pub(crate) enum CheckedInvocation<'a> {
    WaitFor(&'static str),
    Execute(&'static str),
    Rpc(&'a RegisteredRpc),
}

impl RegisteredLockRequirements {
    /// Returns the requirements after validating that each lock Attribute is a named singleton
    /// Attribute of the same Flow, or a [`SdkError::FlowDefinition`] naming the first violation.
    pub(crate) fn assemble<'a>(
        flow_name: &str,
        declared: &[DeclaredLockRequirement],
        persistence: &HashMap<String, PersistenceDefinition>,
        step_options: impl IntoIterator<Item = (&'static str, &'a ErasedStepOptions)>,
    ) -> SdkResult<Self> {
        let mut requirements = HashMap::new();
        for declaration in declared {
            if let LockRequirement::Attribute(lock_name) = &declaration.requirement {
                validate_lock_attribute(lock_name, persistence).map_err(|reason| {
                    SdkError::FlowDefinition {
                        message: format!(
                            "Flow {flow_name} {} {:?} {} lock: {reason}",
                            persistence_kind_label(declaration.kind),
                            declaration.definition_name,
                            if is_channel_kind(declaration.kind) {
                                "publish"
                            } else {
                                "write"
                            },
                        ),
                    }
                })?;
            }
            requirements.insert(
                declaration.definition_name.clone(),
                declaration.requirement.clone(),
            );
        }
        let step_lock_keys = step_options
            .into_iter()
            .map(|(step_type, options)| {
                let keys = StepLockKeys {
                    wait_for: lock_keys(&options.wait_for_locks),
                    execute: lock_keys(&options.execute_locks),
                };
                (step_type, keys)
            })
            .collect();
        Ok(Self {
            requirements,
            step_lock_keys,
        })
    }

    /// Returns `Ok(())` when every buffered write and publish of a declared definition holds its
    /// required lock among `invocation`'s registration-time locks.
    ///
    /// Returns a [`HandlerError`] of type `dex_sdk::MissingAttributeLockError` otherwise.
    pub(crate) fn require_registered_locks(
        &self,
        flow_type: &str,
        invocation: CheckedInvocation<'_>,
        context: &Context,
    ) -> HandlerResult<()> {
        if self.requirements.is_empty() {
            return Ok(());
        }
        let rpc_lock_keys;
        let held_lock_keys = match invocation {
            CheckedInvocation::WaitFor(step_type) => self
                .step_lock_keys
                .get(step_type)
                .map(|keys| &keys.wait_for),
            CheckedInvocation::Execute(step_type) => {
                self.step_lock_keys.get(step_type).map(|keys| &keys.execute)
            }
            CheckedInvocation::Rpc(rpc) => {
                rpc_lock_keys = lock_keys(&rpc.locks);
                Some(&rpc_lock_keys)
            }
        };
        let accesses = self.missing_lock_accesses(held_lock_keys, context);
        if accesses.is_empty() {
            return Ok(());
        }
        let error = MissingAttributeLockError {
            flow_type: flow_type.to_owned(),
            invocation: match invocation {
                CheckedInvocation::WaitFor(step_type) => {
                    MissingAttributeLockInvocation::WaitForMethod {
                        step_type: step_type.to_owned(),
                    }
                }
                CheckedInvocation::Execute(step_type) => {
                    MissingAttributeLockInvocation::ExecuteMethod {
                        step_type: step_type.to_owned(),
                    }
                }
                CheckedInvocation::Rpc(rpc) => MissingAttributeLockInvocation::RpcMethod {
                    rpc_name: rpc.name.to_owned(),
                },
            },
            accesses,
        };
        Err(HandlerError::new(
            MISSING_ATTRIBUTE_LOCK_ERROR_TYPE,
            error.to_string(),
        ))
    }

    fn missing_lock_accesses(
        &self,
        held_lock_keys: Option<&HashSet<String>>,
        context: &Context,
    ) -> Vec<MissingAttributeLockAccess> {
        let is_held = |lock_key: &str| held_lock_keys.is_some_and(|keys| keys.contains(lock_key));
        let mut missing = Vec::new();
        for target in context.attribute_write_targets() {
            let Some(requirement) = self.requirements.get(&target.name) else {
                continue;
            };
            let (lock_key, lock_attribute_name, lock_instance) = match requirement {
                LockRequirement::OwnKey => (
                    target.physical_name.as_str(),
                    target.name.clone(),
                    target.instance.clone(),
                ),
                LockRequirement::Attribute(lock_name) => {
                    (lock_name.as_str(), lock_name.clone(), None)
                }
            };
            if is_held(lock_key) {
                continue;
            }
            missing.push(MissingAttributeLockAccess {
                kind: if target.instance.is_some() {
                    MissingAttributeLockAccessKind::AttributeMapWrite
                } else {
                    MissingAttributeLockAccessKind::AttributeWrite
                },
                name: target.name.clone(),
                instance: target.instance.clone(),
                lock_attribute_name,
                lock_instance,
            });
        }
        for target in context.publish_targets() {
            let Some(LockRequirement::Attribute(lock_name)) = self.requirements.get(&target.name)
            else {
                continue;
            };
            if is_held(lock_name) {
                continue;
            }
            missing.push(MissingAttributeLockAccess {
                kind: if target.instance.is_some() {
                    MissingAttributeLockAccessKind::ChannelMapPublish
                } else {
                    MissingAttributeLockAccessKind::ChannelPublish
                },
                name: target.name.clone(),
                instance: target.instance.clone(),
                lock_attribute_name: lock_name.clone(),
                lock_instance: None,
            });
        }
        missing
    }
}

fn lock_keys(locks: &[AttributeLock]) -> HashSet<String> {
    locks.iter().map(AttributeLock::physical_name).collect()
}

fn validate_lock_attribute(
    lock_name: &str,
    persistence: &HashMap<String, PersistenceDefinition>,
) -> Result<(), String> {
    if lock_name.is_empty() {
        return Err("lock Attribute has an empty name; pass a named Attribute".to_owned());
    }
    match persistence.get(lock_name).map(|definition| definition.kind) {
        Some(PersistenceKind::Attribute) => Ok(()),
        None => Err(format!(
            "lock Attribute {lock_name:?} is not in the PersistenceSchema"
        )),
        Some(PersistenceKind::AttributeMap) => Err(format!(
            "lock Attribute {lock_name:?} is an AttributeMap; use a singleton Attribute"
        )),
        Some(kind) => Err(format!(
            "lock Attribute {lock_name:?} is a {}; use a singleton Attribute",
            persistence_kind_label(kind)
        )),
    }
}

fn is_channel_kind(kind: PersistenceKind) -> bool {
    matches!(kind, PersistenceKind::Channel | PersistenceKind::ChannelMap)
}

fn persistence_kind_label(kind: PersistenceKind) -> &'static str {
    match kind {
        PersistenceKind::Attribute => "Attribute",
        PersistenceKind::AttributeMap => "AttributeMap",
        PersistenceKind::Channel => "Channel",
        PersistenceKind::ChannelMap => "ChannelMap",
        PersistenceKind::Stream => "Stream",
    }
}

/// Reports a handler invocation that wrote a declared Attribute, or published to a declared
/// Channel, without registering the lock that the definition requires.
///
/// A definition declares its lock requirement with [`Attribute::writes_require_lock`],
/// [`Attribute::writes_require_attribute_lock`], the matching [`AttributeMap`] methods,
/// [`Channel::publishes_require_attribute_lock`], or
/// [`ChannelMap::publishes_require_attribute_lock`]. After a `wait_for`, `execute`, or RPC
/// handler returns, the Worker compares each such write and publish with the locks that the
/// handler registered. When a required lock is missing, the Worker fails the invocation with
/// this error. Dex then discards the Attribute writes, Channel publications, and other changes
/// buffered for the invocation's result. Stream writes that the Step already sent and external
/// side effects stay.
///
/// Dex reports it like any other handler error. It retries a Step method under the Step's retry
/// policy and returns the failure to the RPC caller. A retry runs the same handler with the same
/// registered locks, so it fails again unless the handler takes another path.
///
/// Fix the handler registration. Add the lock with [`StepOptions::wait_for_lock`],
/// [`StepOptions::execute_lock`], or [`RpcDefinition::lock`]. Locks added for one call, such as
/// [`RpcInvokeOptions::lock_attribute_map_instance`] or the options of
/// [`StepMovement::to_with_options`], do not satisfy the requirement.
///
/// The value exists only inside the Worker. The Worker reports it with the error type
/// `dex_sdk::MissingAttributeLockError` and this error's message. A Client that invokes the RPC
/// receives [`SdkError::WorkerInvocation`], whose [`WorkerError::error_type`] is that type. A
/// recovery Step reads the same type from [`RecoveryErrorInfo::error_type`].
///
/// # Examples
///
/// ```no_run
/// use dex_sdk::{Client, Rpc, SdkError};
///
/// const ADD_ITEM: Rpc<String, ()> = Rpc::new("add_item");
///
/// fn add_item(client: &Client, flow_id: &str, item: String) {
///     match client.invoke_rpc(flow_id, ADD_ITEM, item) {
///         Err(SdkError::WorkerInvocation { worker, .. })
///             if worker.error_type() == "dex_sdk::MissingAttributeLockError" =>
///         {
///             eprintln!("register the required lock on the RPC: {}", worker.detail());
///         }
///         Err(error) => eprintln!("add_item failed: {error}"),
///         Ok(()) => {}
///     }
/// }
/// ```
///
/// Since 1.45.9.
///
/// [`Attribute::writes_require_lock`]: crate::Attribute::writes_require_lock
/// [`Attribute::writes_require_attribute_lock`]: crate::Attribute::writes_require_attribute_lock
/// [`AttributeMap`]: crate::AttributeMap
/// [`Channel::publishes_require_attribute_lock`]: crate::Channel::publishes_require_attribute_lock
/// [`ChannelMap::publishes_require_attribute_lock`]: crate::ChannelMap::publishes_require_attribute_lock
/// [`StepOptions::wait_for_lock`]: crate::StepOptions::wait_for_lock
/// [`StepOptions::execute_lock`]: crate::StepOptions::execute_lock
/// [`RpcDefinition::lock`]: crate::RpcDefinition::lock
/// [`RpcInvokeOptions::lock_attribute_map_instance`]: crate::RpcInvokeOptions::lock_attribute_map_instance
/// [`StepMovement::to_with_options`]: crate::StepMovement::to_with_options
/// [`SdkError::WorkerInvocation`]: crate::SdkError::WorkerInvocation
/// [`WorkerError::error_type`]: crate::WorkerError::error_type
/// [`RecoveryErrorInfo::error_type`]: crate::RecoveryErrorInfo::error_type
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct MissingAttributeLockError {
    flow_type: String,
    invocation: MissingAttributeLockInvocation,
    accesses: Vec<MissingAttributeLockAccess>,
}

impl MissingAttributeLockError {
    /// Returns the Flow type whose handler ran.
    pub fn flow_type(&self) -> &str {
        &self.flow_type
    }

    /// Returns the handler invocation that wrote or published without the required lock.
    pub fn invocation(&self) -> &MissingAttributeLockInvocation {
        &self.invocation
    }

    /// Returns every write and publish whose required lock was missing.
    ///
    /// Attribute writes come first, in the order the handler first wrote each key. Channel
    /// publishes follow, in the order the handler first published to each Channel or ChannelMap
    /// instance.
    pub fn accesses(&self) -> &[MissingAttributeLockAccess] {
        &self.accesses
    }
}

impl Display for MissingAttributeLockError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        let flow_type = &self.flow_type;
        match &self.invocation {
            MissingAttributeLockInvocation::WaitForMethod { step_type } => {
                write!(formatter, "Flow {flow_type:?} Step {step_type:?} wait_for")?;
            }
            MissingAttributeLockInvocation::ExecuteMethod { step_type } => {
                write!(formatter, "Flow {flow_type:?} Step {step_type:?} execute")?;
            }
            MissingAttributeLockInvocation::RpcMethod { rpc_name } => {
                write!(formatter, "Flow {flow_type:?} RPC {rpc_name:?}")?;
            }
        }
        for (index, access) in self.accesses.iter().enumerate() {
            let separator = if index == 0 { " " } else { ", " };
            write!(formatter, "{separator}{access}")?;
        }
        formatter.write_str(
            "; register the lock with StepOptions::wait_for_lock, StepOptions::execute_lock, or \
             RpcDefinition::lock",
        )
    }
}

impl Error for MissingAttributeLockError {}

/// Identifies the handler invocation of a [`MissingAttributeLockError`].
///
/// Since 1.45.9.
#[derive(Clone, Debug, Eq, PartialEq)]
#[non_exhaustive]
pub enum MissingAttributeLockInvocation {
    /// A Step's `wait_for` method.
    WaitForMethod {
        /// The Step type.
        step_type: String,
    },
    /// A Step's `execute` method.
    ExecuteMethod {
        /// The Step type.
        step_type: String,
    },
    /// An RPC handler.
    RpcMethod {
        /// The RPC name.
        rpc_name: String,
    },
}

/// Names one write or publish whose required lock the handler did not register.
///
/// Since 1.45.9.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct MissingAttributeLockAccess {
    kind: MissingAttributeLockAccessKind,
    name: String,
    instance: Option<String>,
    lock_attribute_name: String,
    lock_instance: Option<String>,
}

impl MissingAttributeLockAccess {
    /// Returns whether the handler wrote an Attribute or AttributeMap instance, or published to a
    /// Channel or ChannelMap instance.
    pub fn kind(&self) -> MissingAttributeLockAccessKind {
        self.kind
    }

    /// Returns the Attribute, AttributeMap, Channel, or ChannelMap name.
    pub fn name(&self) -> &str {
        &self.name
    }

    /// Returns the AttributeMap or ChannelMap instance, or `None` for a singleton definition.
    pub fn instance(&self) -> Option<&str> {
        self.instance.as_deref()
    }

    /// Returns the name of the Attribute or AttributeMap whose lock is required.
    pub fn lock_attribute_name(&self) -> &str {
        &self.lock_attribute_name
    }

    /// Returns the AttributeMap instance whose lock is required, or `None` when the required lock
    /// is a singleton Attribute's lock.
    pub fn lock_instance(&self) -> Option<&str> {
        self.lock_instance.as_deref()
    }
}

impl Display for MissingAttributeLockAccess {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        let operation = match self.kind {
            MissingAttributeLockAccessKind::ChannelPublish
            | MissingAttributeLockAccessKind::ChannelMapPublish => "published to",
            MissingAttributeLockAccessKind::AttributeWrite
            | MissingAttributeLockAccessKind::AttributeMapWrite => "wrote",
        };
        write!(formatter, "{operation} {} {:?}", self.kind, self.name)?;
        if let Some(instance) = &self.instance {
            write!(formatter, " instance {instance:?}")?;
        }
        let lock_attribute_name = &self.lock_attribute_name;
        match &self.lock_instance {
            Some(lock_instance) => write!(
                formatter,
                " without the required lock of AttributeMap {lock_attribute_name:?} instance \
                 {lock_instance:?}"
            ),
            None => write!(
                formatter,
                " without the required lock of Attribute {lock_attribute_name:?}"
            ),
        }
    }
}

/// Classifies one [`MissingAttributeLockAccess`].
///
/// Its [`Display`] form is the definition kind: `Attribute`, `AttributeMap`, `Channel`, or
/// `ChannelMap`.
///
/// Since 1.45.9.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
#[non_exhaustive]
pub enum MissingAttributeLockAccessKind {
    /// A write or delete of a singleton Attribute.
    AttributeWrite,
    /// A write or delete of one AttributeMap instance.
    AttributeMapWrite,
    /// A publish to a singleton Channel.
    ChannelPublish,
    /// A publish to one ChannelMap instance.
    ChannelMapPublish,
}

impl Display for MissingAttributeLockAccessKind {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(match self {
            Self::AttributeWrite => "Attribute",
            Self::AttributeMapWrite => "AttributeMap",
            Self::ChannelPublish => "Channel",
            Self::ChannelMapPublish => "ChannelMap",
        })
    }
}

#[cfg(test)]
mod tests;
