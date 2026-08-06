// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::collections::{HashMap, HashSet};
use std::error::Error;
use std::fmt::{Display, Formatter};
use std::sync::Arc;

/// Language-neutral registry shared by transport and bridges.
#[derive(Clone, Debug)]
pub struct Registry {
    flows: Arc<HashMap<String, FlowSpec>>,
}

/// One registered Flow and its durable definitions.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FlowSpec {
    name: String,
    steps: Vec<StepSpec>,
    rpcs: Vec<RpcSpec>,
    persistence: Vec<PersistenceSpec>,
}

/// One registered Step.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct StepSpec {
    name: String,
    starting: bool,
}

/// One registered RPC.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RpcSpec {
    name: String,
}

/// One registered persistence definition.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct PersistenceSpec {
    name: String,
    kind: PersistenceKind,
}

/// Persistence definition shape relevant to Core validation.
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub enum PersistenceKind {
    Attribute,
    AttributeMap,
    Channel,
    ChannelMap,
}

/// Atomic registry assembly failure.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RegistryError(String);

impl Registry {
    /// Validates and assembles an immutable Registry.
    pub fn new(flows: Vec<FlowSpec>) -> Result<Self, RegistryError> {
        let mut registered = HashMap::with_capacity(flows.len());
        for flow in flows {
            flow.validate()?;
            if registered.insert(flow.name.clone(), flow).is_some() {
                return Err(RegistryError::new("duplicate Flow durable name"));
            }
        }
        Ok(Self {
            flows: Arc::new(registered),
        })
    }

    /// Looks up a Flow by durable name.
    pub fn flow(&self, name: &str) -> Option<&FlowSpec> {
        self.flows.get(name)
    }

    /// Returns the number of registered Flows.
    pub fn len(&self) -> usize {
        self.flows.len()
    }

    /// Reports whether no Flows are registered.
    pub fn is_empty(&self) -> bool {
        self.flows.is_empty()
    }
}

impl FlowSpec {
    /// Creates a Flow specification for atomic Registry validation.
    pub fn new(
        name: impl Into<String>,
        steps: Vec<StepSpec>,
        rpcs: Vec<RpcSpec>,
        persistence: Vec<PersistenceSpec>,
    ) -> Self {
        Self {
            name: name.into(),
            steps,
            rpcs,
            persistence,
        }
    }

    /// Returns the Flow durable name.
    pub fn name(&self) -> &str {
        &self.name
    }

    /// Looks up a Step by durable name.
    pub fn step(&self, name: &str) -> Option<&StepSpec> {
        self.steps.iter().find(|step| step.name == name)
    }

    /// Looks up an RPC by durable name.
    pub fn rpc(&self, name: &str) -> Option<&RpcSpec> {
        self.rpcs.iter().find(|rpc| rpc.name == name)
    }

    /// Looks up persistence by durable name and kind.
    pub fn persistence(&self, name: &str, kind: PersistenceKind) -> Option<&PersistenceSpec> {
        self.persistence
            .iter()
            .find(|definition| definition.name == name && definition.kind == kind)
    }

    /// Returns the optional starting Step.
    pub fn starting_step(&self) -> Option<&StepSpec> {
        self.steps.iter().find(|step| step.starting)
    }

    fn validate(&self) -> Result<(), RegistryError> {
        validate_name("Flow", &self.name)?;
        validate_definitions("Step", &self.steps, StepSpec::name)?;
        validate_definitions("RPC", &self.rpcs, RpcSpec::name)?;

        let starting_steps = self.steps.iter().filter(|step| step.starting).count();
        if starting_steps > 1 {
            return Err(RegistryError::new(format!(
                "Flow {:?} has multiple starting Steps",
                self.name
            )));
        }

        let mut persistence = HashSet::with_capacity(self.persistence.len());
        for definition in &self.persistence {
            validate_name("persistence definition", &definition.name)?;
            if !persistence.insert((definition.kind, definition.name.as_str())) {
                return Err(RegistryError::new(format!(
                    "Flow {:?} has duplicate {:?} {:?}",
                    self.name, definition.kind, definition.name
                )));
            }
        }
        Ok(())
    }
}

impl StepSpec {
    /// Creates a starting Step specification.
    pub fn starting(name: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            starting: true,
        }
    }

    /// Creates a non-starting Step specification.
    pub fn non_starting(name: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            starting: false,
        }
    }

    /// Returns the Step durable name.
    pub fn name(&self) -> &str {
        &self.name
    }

    /// Reports whether this is the starting Step.
    pub fn is_starting(&self) -> bool {
        self.starting
    }
}

impl RpcSpec {
    /// Creates an RPC specification.
    pub fn new(name: impl Into<String>) -> Self {
        Self { name: name.into() }
    }

    /// Returns the RPC durable name.
    pub fn name(&self) -> &str {
        &self.name
    }
}

impl PersistenceSpec {
    /// Creates a persistence specification.
    pub fn new(name: impl Into<String>, kind: PersistenceKind) -> Self {
        Self {
            name: name.into(),
            kind,
        }
    }

    /// Returns the persistence durable name.
    pub fn name(&self) -> &str {
        &self.name
    }

    /// Returns the persistence definition kind.
    pub fn kind(&self) -> PersistenceKind {
        self.kind
    }
}

impl RegistryError {
    fn new(message: impl Into<String>) -> Self {
        Self(message.into())
    }
}

impl Display for RegistryError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(&self.0)
    }
}

impl Error for RegistryError {}

fn validate_definitions<T>(
    kind: &str,
    definitions: &[T],
    name: impl Fn(&T) -> &str,
) -> Result<(), RegistryError> {
    let mut names = HashSet::with_capacity(definitions.len());
    for definition in definitions {
        let durable_name = name(definition);
        validate_name(kind, durable_name)?;
        if !names.insert(durable_name) {
            return Err(RegistryError::new(format!(
                "duplicate {kind} durable name {durable_name:?}"
            )));
        }
    }
    Ok(())
}

fn validate_name(kind: &str, name: &str) -> Result<(), RegistryError> {
    if name.is_empty() || name.trim() != name {
        return Err(RegistryError::new(format!(
            "{kind} durable name must be non-empty without surrounding whitespace"
        )));
    }
    Ok(())
}
