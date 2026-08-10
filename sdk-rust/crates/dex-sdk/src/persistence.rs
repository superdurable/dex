// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use crate::{Attribute, AttributeMap, Channel, ChannelMap};

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct PersistenceSchema {
    definitions: Vec<PersistenceDefinition>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct PersistenceDefinition {
    pub(crate) name: String,
    pub(crate) kind: PersistenceKind,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum PersistenceKind {
    Attribute,
    AttributeMap,
    Channel,
    ChannelMap,
}

impl PersistenceSchema {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn attribute<T>(mut self, attribute: &Attribute<T>) -> Self {
        self.add(attribute.name(), PersistenceKind::Attribute);
        self
    }

    pub fn attribute_map<T>(mut self, attribute: &AttributeMap<T>) -> Self {
        self.add(attribute.name(), PersistenceKind::AttributeMap);
        self
    }

    pub fn channel<T>(mut self, channel: &Channel<T>) -> Self {
        self.add(channel.name(), PersistenceKind::Channel);
        self
    }

    pub fn channel_map<T>(mut self, channel: &ChannelMap<T>) -> Self {
        self.add(channel.name(), PersistenceKind::ChannelMap);
        self
    }

    pub(crate) fn definitions(&self) -> &[PersistenceDefinition] {
        &self.definitions
    }

    fn add(&mut self, name: &str, kind: PersistenceKind) {
        self.definitions.push(PersistenceDefinition {
            name: name.to_string(),
            kind,
        });
    }
}
