// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use crate::{Attribute, AttributeIndex, AttributeMap, Channel, ChannelMap, Stream};

#[derive(Clone, Debug, Default, Eq, PartialEq)]
/// Declares the Attributes, Channels, and Streams a Flow owns.
///
/// Return a schema from [`crate::Flow::persistence`]. Definitions must have unique names,
/// and all values accessed by Step or RPC code must appear in the schema.
///
/// # Examples
///
/// ```
/// use dex_sdk::{Attribute, Channel, PersistenceSchema};
///
/// let status = Attribute::<String>::new("status");
/// let commands = Channel::<String>::new("commands");
/// let schema = PersistenceSchema::new().attribute(&status).channel(&commands);
/// ```
pub struct PersistenceSchema {
    definitions: Vec<PersistenceDefinition>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct PersistenceDefinition {
    pub(crate) name: String,
    pub(crate) kind: PersistenceKind,
    pub(crate) index: Option<AttributeIndex>,
    pub(crate) sync_to_attribute_store: bool,
    pub(crate) stream_identity: Option<usize>,
    pub(crate) max_estimated_bytes: Option<i64>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum PersistenceKind {
    Attribute,
    AttributeMap,
    Channel,
    ChannelMap,
    Stream,
}

impl PersistenceSchema {
    /// Creates an empty schema.
    pub fn new() -> Self {
        Self::default()
    }

    /// Adds one single-value Attribute definition.
    pub fn attribute<T>(mut self, attribute: &Attribute<T>) -> Self {
        self.add(
            attribute.name(),
            PersistenceKind::Attribute,
            attribute.index().cloned(),
            attribute.is_sync_to_attribute_store(),
            None,
            None,
        );
        self
    }

    /// Adds one keyed Attribute-map definition.
    pub fn attribute_map<T>(mut self, attribute: &AttributeMap<T>) -> Self {
        self.add(
            attribute.name(),
            PersistenceKind::AttributeMap,
            attribute.index().cloned(),
            attribute.is_sync_to_attribute_store(),
            None,
            None,
        );
        self
    }

    /// Adds one Channel definition.
    pub fn channel<T>(mut self, channel: &Channel<T>) -> Self {
        self.add(
            channel.name(),
            PersistenceKind::Channel,
            None,
            false,
            None,
            None,
        );
        self
    }

    /// Adds one keyed Channel-map definition.
    pub fn channel_map<T>(mut self, channel: &ChannelMap<T>) -> Self {
        self.add(
            channel.name(),
            PersistenceKind::ChannelMap,
            None,
            false,
            None,
            None,
        );
        self
    }

    /// Adds one best-effort Stream definition.
    pub fn stream<T>(mut self, stream: &Stream<T>) -> Self {
        self.add(
            stream.name(),
            PersistenceKind::Stream,
            None,
            false,
            Some(stream.identity()),
            Some(stream.max_estimated_bytes()),
        );
        self
    }

    pub(crate) fn definitions(&self) -> &[PersistenceDefinition] {
        &self.definitions
    }

    fn add(
        &mut self,
        name: &str,
        kind: PersistenceKind,
        index: Option<AttributeIndex>,
        sync_to_attribute_store: bool,
        stream_identity: Option<usize>,
        max_estimated_bytes: Option<i64>,
    ) {
        self.definitions.push(PersistenceDefinition {
            name: name.to_string(),
            kind,
            index,
            sync_to_attribute_store,
            stream_identity,
            max_estimated_bytes,
        });
    }
}

#[cfg(test)]
mod tests {
    use super::PersistenceSchema;
    use crate::{Attribute, AttributeMap, Stream};

    #[test]
    fn registration_metadata_retains_sync_configuration() {
        let attribute = Attribute::<String>::new("plain");
        let attribute_map = AttributeMap::<String>::new("map").sync_to_attribute_store();
        let schema = PersistenceSchema::new()
            .attribute(&attribute)
            .attribute_map(&attribute_map);

        assert!(!schema.definitions[0].sync_to_attribute_store);
        assert!(schema.definitions[1].sync_to_attribute_store);
    }

    #[test]
    fn registration_metadata_retains_stream_identity_and_capacity() {
        let stream = Stream::<String>::new("thinking", 1_048_576);
        let schema = PersistenceSchema::new().stream(&stream);

        assert_eq!(
            schema.definitions[0].stream_identity,
            Some(stream.identity())
        );
        assert_eq!(schema.definitions[0].max_estimated_bytes, Some(1_048_576));
    }
}
