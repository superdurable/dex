// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use crate::{Attribute, AttributeIndex, AttributeMap, Channel, ChannelMap, Stream};

#[derive(Clone, Debug, Default)]
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

// Schema equality predates value type tracking, so it keeps ignoring value types.
impl PartialEq for PersistenceSchema {
    fn eq(&self, other: &Self) -> bool {
        self.definitions.len() == other.definitions.len()
            && self.definitions.iter().zip(&other.definitions).all(
                |(definition, other_definition)| {
                    definition.is_equal_ignoring_value_type(other_definition)
                },
            )
    }
}

impl Eq for PersistenceSchema {}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct PersistenceDefinition {
    pub(crate) name: String,
    pub(crate) kind: PersistenceKind,
    pub(crate) index: Option<AttributeIndex>,
    pub(crate) sync_to_attribute_store: bool,
    pub(crate) is_hard_delete_on_null: bool,
    pub(crate) numeric_or_boolean_type_name: Option<&'static str>,
    pub(crate) value_type_name: Option<&'static str>,
    pub(crate) stream_identity: Option<usize>,
    pub(crate) stream_capacity_bytes: Option<i64>,
}

impl PersistenceDefinition {
    pub(crate) fn new(name: &str, kind: PersistenceKind) -> Self {
        Self {
            name: name.to_string(),
            kind,
            index: None,
            sync_to_attribute_store: false,
            is_hard_delete_on_null: false,
            numeric_or_boolean_type_name: None,
            value_type_name: None,
            stream_identity: None,
            stream_capacity_bytes: None,
        }
    }

    fn is_equal_ignoring_value_type(&self, other: &Self) -> bool {
        let Self {
            name,
            kind,
            index,
            sync_to_attribute_store,
            is_hard_delete_on_null,
            numeric_or_boolean_type_name,
            value_type_name: _,
            stream_identity,
            stream_capacity_bytes,
        } = self;
        *name == other.name
            && *kind == other.kind
            && *index == other.index
            && *sync_to_attribute_store == other.sync_to_attribute_store
            && *is_hard_delete_on_null == other.is_hard_delete_on_null
            && *numeric_or_boolean_type_name == other.numeric_or_boolean_type_name
            && *stream_identity == other.stream_identity
            && *stream_capacity_bytes == other.stream_capacity_bytes
    }

    pub(crate) fn for_attribute<T>(attribute: &Attribute<T>) -> Self {
        Self {
            index: attribute.index().cloned(),
            sync_to_attribute_store: attribute.is_sync_to_attribute_store(),
            is_hard_delete_on_null: attribute.is_hard_delete_on_null(),
            numeric_or_boolean_type_name: attribute.numeric_or_boolean_type_name(),
            value_type_name: Some(std::any::type_name::<T>()),
            ..Self::new(attribute.name(), PersistenceKind::Attribute)
        }
    }

    pub(crate) fn for_attribute_map<T>(attribute_map: &AttributeMap<T>) -> Self {
        Self {
            index: attribute_map.index().cloned(),
            sync_to_attribute_store: attribute_map.is_sync_to_attribute_store(),
            is_hard_delete_on_null: attribute_map.is_hard_delete_on_null(),
            numeric_or_boolean_type_name: attribute_map.numeric_or_boolean_type_name(),
            value_type_name: Some(std::any::type_name::<T>()),
            ..Self::new(attribute_map.name(), PersistenceKind::AttributeMap)
        }
    }
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
        self.definitions
            .push(PersistenceDefinition::for_attribute(attribute));
        self
    }

    /// Adds one keyed Attribute-map definition.
    pub fn attribute_map<T>(mut self, attribute: &AttributeMap<T>) -> Self {
        self.definitions
            .push(PersistenceDefinition::for_attribute_map(attribute));
        self
    }

    /// Adds one Channel definition.
    pub fn channel<T>(mut self, channel: &Channel<T>) -> Self {
        self.definitions.push(PersistenceDefinition::new(
            channel.name(),
            PersistenceKind::Channel,
        ));
        self
    }

    /// Adds one keyed Channel-map definition.
    pub fn channel_map<T>(mut self, channel: &ChannelMap<T>) -> Self {
        self.definitions.push(PersistenceDefinition::new(
            channel.name(),
            PersistenceKind::ChannelMap,
        ));
        self
    }

    /// Adds one best-effort Stream definition.
    pub fn stream<T>(mut self, stream: &Stream<T>) -> Self {
        self.definitions.push(PersistenceDefinition {
            stream_identity: Some(stream.identity()),
            stream_capacity_bytes: Some(stream.stream_capacity_bytes()),
            ..PersistenceDefinition::new(stream.name(), PersistenceKind::Stream)
        });
        self
    }

    pub(crate) fn definitions(&self) -> &[PersistenceDefinition] {
        &self.definitions
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
    fn registration_metadata_retains_hard_delete_configuration() {
        let plain = Attribute::<String>::new("plain");
        let secret = Attribute::<String>::new("secret").hard_delete_on_null();
        let counter = Attribute::<Option<u64>>::new("counter").hard_delete_on_null();
        let secrets = AttributeMap::<Vec<u8>>::new("secrets").hard_delete_on_null();
        let schema = PersistenceSchema::new()
            .attribute(&plain)
            .attribute(&secret)
            .attribute(&counter)
            .attribute_map(&secrets.clone());

        assert!(!schema.definitions[0].is_hard_delete_on_null);
        assert!(schema.definitions[1].is_hard_delete_on_null);
        assert_eq!(schema.definitions[1].numeric_or_boolean_type_name, None);
        assert!(
            schema.definitions[2]
                .numeric_or_boolean_type_name
                .is_some_and(|type_name| type_name.contains("u64"))
        );
        assert!(schema.definitions[3].is_hard_delete_on_null);
        assert_eq!(schema.definitions[3].numeric_or_boolean_type_name, None);
    }

    #[test]
    fn schema_equality_ignores_attribute_value_types() {
        let text = PersistenceSchema::new().attribute(&Attribute::<String>::new("status"));
        let number = PersistenceSchema::new().attribute(&Attribute::<i64>::new("status"));
        let other_name = PersistenceSchema::new().attribute(&Attribute::<String>::new("state"));

        assert_eq!(text, number);
        assert_ne!(text, other_name);
    }

    #[test]
    fn registration_metadata_retains_stream_identity_and_capacity() {
        let stream = Stream::<String>::new("thinking", 1_048_576);
        let schema = PersistenceSchema::new().stream(&stream);

        assert_eq!(
            schema.definitions[0].stream_identity,
            Some(stream.identity())
        );
        assert_eq!(schema.definitions[0].stream_capacity_bytes, Some(1_048_576));
    }
}
