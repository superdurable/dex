// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::any::TypeId;
use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use crate::persistence::{PersistenceDefinition, PersistenceKind};
use crate::rpc::RegisteredRpc;
use crate::step::RegisteredStep;
use crate::step_options::ErasedStepOptions;
use crate::{Context, Flow, HandlerResult, SdkError, SdkResult, StepDecision, Wait};
use dex_protocol::dex::Value as ProtoValue;

#[derive(Clone, Default)]
/// Validates and stores all Flow definitions used by a Client or Worker.
///
/// Registration checks unique Flow, Step, RPC, and persistence names; starting-Step constraints;
/// Attribute index consistency; RPC locks; and handler options. Cloning is cheap because it shares
/// the registered data.
///
/// # Examples
///
/// ```
/// use dex_sdk::{Flow, Registry};
///
/// struct OrderFlow;
/// impl Flow for OrderFlow { type StartInput = String; }
///
/// let registry = Registry::new().register(OrderFlow)?;
/// # Ok::<(), dex_sdk::SdkError>(())
/// ```
pub struct Registry {
    inner: Arc<RegistryInner>,
}

#[derive(Clone, Default)]
struct RegistryInner {
    flows: HashMap<&'static str, RegisteredFlow>,
    attribute_indexes: HashMap<String, i32>,
}

#[derive(Clone)]
pub(crate) struct RegisteredFlow {
    pub(crate) name: &'static str,
    pub(crate) type_id: TypeId,
    pub(crate) steps: HashMap<&'static str, RegisteredStep>,
    pub(crate) start_step: Option<RegisteredStep>,
    pub(crate) rpcs: HashMap<&'static str, RegisteredRpc>,
    pub(crate) persistence: HashMap<String, PersistenceDefinition>,
    pub(crate) handler: Arc<dyn ErasedFlow>,
}

impl Registry {
    /// Creates an empty registry.
    pub fn new() -> Self {
        Self::default()
    }

    /// Validates and adds one owned Flow definition.
    ///
    /// # Errors
    ///
    /// Returns [`SdkError::FlowDefinition`] for invalid or conflicting names, schemas, indexes,
    /// Steps, RPCs, locks, or options.
    pub fn register<SomeFlow: Flow>(mut self, flow: SomeFlow) -> SdkResult<Self> {
        let flow = Arc::new(flow);
        let name = require_static_name(flow.flow_type(), "Flow type")?;
        let registered = assemble_flow(name, Arc::clone(&flow))?;
        let inner = Arc::make_mut(&mut self.inner);
        let mut additions = Vec::new();
        for definition in registered.persistence.values() {
            let Some(index) = &definition.index else {
                continue;
            };
            if definition.kind == PersistenceKind::AttributeMap
                && index.key().is_none_or(str::is_empty)
            {
                return Err(definition_error(format!(
                    "Flow {name} indexed AttributeMap {} requires an index key",
                    definition.name
                )));
            }
            let key = index.key().unwrap_or(&definition.name).to_string();
            let index_type = index.proto_value();
            if inner
                .attribute_indexes
                .get(&key)
                .is_some_and(|existing| *existing != index_type)
            {
                return Err(definition_error(format!(
                    "Attribute index {key} has conflicting types"
                )));
            }
            additions.push((key, index_type));
        }
        if inner.flows.insert(name, registered).is_some() {
            return Err(definition_error(format!("duplicate Flow {name}")));
        }
        inner.attribute_indexes.extend(additions);
        Ok(self)
    }

    pub(crate) fn flow(&self, name: &str) -> SdkResult<&RegisteredFlow> {
        self.inner
            .flows
            .get(name)
            .ok_or_else(|| SdkError::FlowDefinition {
                message: format!("Flow is not registered: {name}"),
            })
    }

    pub(crate) fn rpc(&self, name: &str) -> SdkResult<&RegisteredRpc> {
        let mut matched = None;
        for flow in self.inner.flows.values() {
            let Some(rpc) = flow.rpcs.get(name) else {
                continue;
            };
            if matched.is_some() {
                return Err(SdkError::FlowDefinition {
                    message: format!("RPC name is ambiguous across registered Flows: {name}"),
                });
            }
            matched = Some(rpc);
        }
        matched.ok_or_else(|| SdkError::FlowDefinition {
            message: format!("RPC is not registered: {name}"),
        })
    }

    pub(crate) fn attribute_indexes(&self) -> &HashMap<String, i32> {
        &self.inner.attribute_indexes
    }
}

fn assemble_flow<SomeFlow: Flow>(
    name: &'static str,
    flow: Arc<SomeFlow>,
) -> SdkResult<RegisteredFlow> {
    let mut steps = HashMap::new();
    let mut start_step = None;
    for step in flow.steps().into_definitions() {
        require_static_name(step.name, "Step type")?;
        if step.starting {
            if start_step.is_some() {
                return Err(definition_error(format!(
                    "Flow {name} must not have multiple start Steps"
                )));
            }
            start_step = Some(step.clone());
        }
        if steps.insert(step.name, step).is_some() {
            return Err(definition_error(format!(
                "Flow {name} has a duplicate Step"
            )));
        }
    }

    let persistence = assemble_persistence(name, flow.persistence().definitions())?;
    let mut rpcs = HashMap::new();
    for rpc in flow.rpcs().bind(Arc::clone(&flow)) {
        require_static_name(rpc.name, "RPC name")?;
        validate_rpc(name, &rpc, &persistence)?;
        if rpcs.insert(rpc.name, rpc).is_some() {
            return Err(definition_error(format!("Flow {name} has a duplicate RPC")));
        }
    }
    Ok(RegisteredFlow {
        name,
        type_id: TypeId::of::<SomeFlow>(),
        steps,
        start_step,
        rpcs,
        persistence,
        handler: Arc::new(TypedFlow { flow }),
    })
}

pub(crate) trait ErasedFlow: Send + Sync {
    fn wait_for(
        &self,
        step_type: &str,
        context: &mut Context,
        input: &ProtoValue,
    ) -> HandlerResult<Wait>;
    fn execute(
        &self,
        step_type: &str,
        context: &mut Context,
        input: &ProtoValue,
    ) -> HandlerResult<StepDecision>;
    fn step_options(&self, step_type: &str) -> HandlerResult<ErasedStepOptions>;
}

struct TypedFlow<SomeFlow> {
    flow: Arc<SomeFlow>,
}

impl<SomeFlow: Flow> ErasedFlow for TypedFlow<SomeFlow> {
    fn wait_for(
        &self,
        step_type: &str,
        context: &mut Context,
        input: &ProtoValue,
    ) -> HandlerResult<Wait> {
        self.flow
            .steps()
            .find(step_type)
            .ok_or_else(|| {
                crate::HandlerError::new(format!("Step is not registered: {step_type}"))
            })?
            .wait_for(context, input)
    }

    fn execute(
        &self,
        step_type: &str,
        context: &mut Context,
        input: &ProtoValue,
    ) -> HandlerResult<StepDecision> {
        self.flow
            .steps()
            .find(step_type)
            .ok_or_else(|| {
                crate::HandlerError::new(format!("Step is not registered: {step_type}"))
            })?
            .execute(context, input)
    }

    fn step_options(&self, step_type: &str) -> HandlerResult<ErasedStepOptions> {
        Ok(self
            .flow
            .steps()
            .find(step_type)
            .ok_or_else(|| {
                crate::HandlerError::new(format!("Step is not registered: {step_type}"))
            })?
            .options())
    }
}

fn assemble_persistence(
    flow_name: &str,
    definitions: &[PersistenceDefinition],
) -> SdkResult<HashMap<String, PersistenceDefinition>> {
    let mut persistence = HashMap::new();
    for definition in definitions {
        if definition.name.is_empty() {
            return Err(definition_error(format!(
                "Flow {flow_name} has an empty persistence definition name"
            )));
        }
        if persistence
            .insert(definition.name.clone(), definition.clone())
            .is_some()
        {
            return Err(definition_error(format!(
                "Flow {flow_name} has duplicate persistence definition {}",
                definition.name
            )));
        }
    }
    Ok(persistence)
}

fn validate_rpc(
    flow_name: &str,
    rpc: &RegisteredRpc,
    persistence: &HashMap<String, PersistenceDefinition>,
) -> SdkResult<()> {
    if rpc.timeout.is_some_and(|timeout| timeout.is_zero()) {
        return Err(definition_error(format!(
            "Flow {flow_name} RPC {} timeout must be positive",
            rpc.name
        )));
    }
    let mut locks = HashSet::new();
    for lock in &rpc.locks {
        let physical_name = lock.physical_name();
        let logical_name = physical_name.split('/').next().unwrap_or(&physical_name);
        let definition = persistence.get(logical_name).ok_or_else(|| {
            definition_error(format!(
                "Flow {flow_name} RPC {} locks an unregistered Attribute",
                rpc.name
            ))
        })?;
        if !matches!(
            definition.kind,
            PersistenceKind::Attribute | PersistenceKind::AttributeMap
        ) {
            return Err(definition_error(format!(
                "Flow {flow_name} RPC {} locks a non-Attribute",
                rpc.name
            )));
        }
        if !locks.insert(physical_name) {
            return Err(definition_error(format!(
                "Flow {flow_name} RPC {} has a duplicate lock",
                rpc.name
            )));
        }
    }
    Ok(())
}

fn require_static_name(name: &'static str, kind: &str) -> SdkResult<&'static str> {
    if name.is_empty() {
        Err(definition_error(format!("{kind} is required")))
    } else {
        Ok(name)
    }
}

fn definition_error(message: impl Into<String>) -> SdkError {
    SdkError::FlowDefinition {
        message: message.into(),
    }
}

pub(crate) fn physical_name(name: &str, instance: &str) -> String {
    if instance.is_empty() {
        panic!("persistence instance is required");
    }
    let mut encoded = String::new();
    for byte in instance.bytes() {
        if byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.' | b'~') {
            encoded.push(char::from(byte));
        } else {
            encoded.push_str(&format!("%{byte:02X}"));
        }
    }
    format!("{name}/{encoded}")
}
