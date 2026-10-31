// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

use dex_protocol::dex::{GetAttributesRequest, Kv, Value as ProtoValue};

use super::{Client, invalid, map_physical_name};
use crate::persistence::PersistenceDefinition;
use crate::sdk_error::{FlowTargetRequirement, ServiceError};
use crate::{Attribute, AttributeMapInstance, SdkError, SdkResult, Value, value_mapper};

const GET_ATTRIBUTES_OPERATION: &str = "get_attributes";

/// Selects one Attribute or AttributeMap instance for [`Client::get_attributes`].
///
/// [`Attribute<T>`] and [`AttributeMapInstance<T>`] implement this trait.
/// References to them and `LazyLock` statics that hold them also implement it.
/// Create an [`AttributeMapInstance`] with [`AttributeMap::instance`](crate::AttributeMap::instance).
/// Applications cannot implement this trait.
/// See [`Client::get_attributes`] for an example.
///
/// An [`AttributeMapLoad`](crate::AttributeMapLoad) from
/// [`AttributeMap::load`](crate::AttributeMap::load) is not a read, so it does not compile:
///
/// ```compile_fail
/// use dex_sdk::{AttributeMap, Client};
///
/// fn read_note(client: &Client, notes: &AttributeMap<String>) -> dex_sdk::SdkResult<()> {
///     client.get_attributes("order-1", (&notes.load("n1"),))?;
///     Ok(())
/// }
/// ```
#[diagnostic::on_unimplemented(
    message = "`{Self}` is not an Attribute read",
    label = "pass an Attribute or `AttributeMap::instance(key)`",
    note = "`AttributeMap::load` selects an instance for a handler Context, not for `Client::get_attributes`"
)]
pub trait AttributeRead: sealed::SealedAttributeRead {
    /// The decoded value type of the selected Attribute or AttributeMap instance.
    type Value: Value;

    #[doc(hidden)]
    fn attribute_read_target(&self) -> sealed::AttributeReadTarget;
}

/// Lists the Attribute reads of one [`Client::get_attributes`] call as a tuple.
///
/// Tuples of 1 to 12 [`AttributeRead`] values implement this trait.
/// Each tuple element is usually a reference, such as `&ORDER_STATUS` or `&NOTES.instance("n1")`.
/// The call returns a tuple of the same arity whose elements are `Option<T>`.
/// Pass a one-element tuple, such as `(&ORDER_STATUS,)`, to read one Attribute.
/// Pass more than 12 reads by making several calls.
/// The empty tuple `()` does not implement this trait, so a call needs at least one read.
/// Applications cannot implement this trait.
/// See [`Client::get_attributes`] for an example.
#[diagnostic::on_unimplemented(
    message = "`{Self}` is not a tuple of 1 to 12 Attribute reads",
    label = "pass a tuple such as `(&ORDER_STATUS, &NOTES.instance(\"n1\"))`",
    note = "use a one-element tuple such as `(&ORDER_STATUS,)` to read one Attribute"
)]
pub trait AttributeReads: sealed::SealedAttributeReads {
    /// The tuple of decoded values, with one `Option<T>` per read in read order.
    type Values;

    #[doc(hidden)]
    fn attribute_read_targets(&self) -> Vec<sealed::AttributeReadTarget>;

    #[doc(hidden)]
    fn decode_attribute_values(
        decoder: &mut sealed::AttributeValueDecoder,
    ) -> SdkResult<Self::Values>;
}

mod sealed {
    use super::*;

    pub trait SealedAttributeRead {}

    pub trait SealedAttributeReads {}

    pub struct AttributeReadTarget {
        pub(super) definition: PersistenceDefinition,
        pub(super) instance: Option<String>,
    }

    pub struct AttributeValueDecoder {
        pub(super) flow_id: String,
        pub(super) values: std::vec::IntoIter<(String, Option<ProtoValue>)>,
    }

    impl AttributeValueDecoder {
        pub(super) fn decode_next<T: Value>(&mut self) -> SdkResult<Option<T>> {
            let (key, value) = self
                .values
                .next()
                .expect("the decoder holds one value slot per read");
            value
                .map(|value| {
                    value_mapper::decode_without_value_detail(&value).map_err(|error| {
                        error.with_call_context(
                            GET_ATTRIBUTES_OPERATION,
                            &self.flow_id,
                            format!(
                                "get_attributes Flow {:?} Attribute {key:?} value cannot be \
                                 decoded as {}",
                                self.flow_id,
                                std::any::type_name::<T>()
                            ),
                        )
                    })
                })
                .transpose()
        }
    }
}

impl<T: Value> sealed::SealedAttributeRead for Attribute<T> {}

impl<T: Value> AttributeRead for Attribute<T> {
    type Value = T;

    fn attribute_read_target(&self) -> sealed::AttributeReadTarget {
        sealed::AttributeReadTarget {
            definition: PersistenceDefinition::for_attribute(self),
            instance: None,
        }
    }
}

impl<T: Value> sealed::SealedAttributeRead for AttributeMapInstance<T> {}

impl<T: Value> AttributeRead for AttributeMapInstance<T> {
    type Value = T;

    fn attribute_read_target(&self) -> sealed::AttributeReadTarget {
        sealed::AttributeReadTarget {
            definition: PersistenceDefinition::for_attribute_map(self.attribute_map()),
            instance: Some(self.instance().to_string()),
        }
    }
}

impl<Read: AttributeRead> sealed::SealedAttributeRead for &Read {}

#[diagnostic::do_not_recommend]
impl<Read: AttributeRead> AttributeRead for &Read {
    type Value = Read::Value;

    fn attribute_read_target(&self) -> sealed::AttributeReadTarget {
        (**self).attribute_read_target()
    }
}

impl<Read: AttributeRead, Init: FnOnce() -> Read> sealed::SealedAttributeRead
    for LazyLock<Read, Init>
{
}

#[diagnostic::do_not_recommend]
impl<Read: AttributeRead, Init: FnOnce() -> Read> AttributeRead for LazyLock<Read, Init> {
    type Value = Read::Value;

    fn attribute_read_target(&self) -> sealed::AttributeReadTarget {
        LazyLock::force(self).attribute_read_target()
    }
}

macro_rules! impl_attribute_reads {
    ($(($index:tt, $read:ident)),+) => {
        impl<$($read: AttributeRead),+> sealed::SealedAttributeReads for ($($read,)+) {}

        #[diagnostic::do_not_recommend]
        impl<$($read: AttributeRead),+> AttributeReads for ($($read,)+) {
            type Values = ($(Option<$read::Value>,)+);

            fn attribute_read_targets(&self) -> Vec<sealed::AttributeReadTarget> {
                vec![$(self.$index.attribute_read_target()),+]
            }

            fn decode_attribute_values(
                decoder: &mut sealed::AttributeValueDecoder,
            ) -> SdkResult<Self::Values> {
                Ok(($(decoder.decode_next::<$read::Value>()?,)+))
            }
        }
    };
}

impl_attribute_reads!((0, Read0));
impl_attribute_reads!((0, Read0), (1, Read1));
impl_attribute_reads!((0, Read0), (1, Read1), (2, Read2));
impl_attribute_reads!((0, Read0), (1, Read1), (2, Read2), (3, Read3));
impl_attribute_reads!((0, Read0), (1, Read1), (2, Read2), (3, Read3), (4, Read4));
impl_attribute_reads!(
    (0, Read0),
    (1, Read1),
    (2, Read2),
    (3, Read3),
    (4, Read4),
    (5, Read5)
);
impl_attribute_reads!(
    (0, Read0),
    (1, Read1),
    (2, Read2),
    (3, Read3),
    (4, Read4),
    (5, Read5),
    (6, Read6)
);
impl_attribute_reads!(
    (0, Read0),
    (1, Read1),
    (2, Read2),
    (3, Read3),
    (4, Read4),
    (5, Read5),
    (6, Read6),
    (7, Read7)
);
impl_attribute_reads!(
    (0, Read0),
    (1, Read1),
    (2, Read2),
    (3, Read3),
    (4, Read4),
    (5, Read5),
    (6, Read6),
    (7, Read7),
    (8, Read8)
);
impl_attribute_reads!(
    (0, Read0),
    (1, Read1),
    (2, Read2),
    (3, Read3),
    (4, Read4),
    (5, Read5),
    (6, Read6),
    (7, Read7),
    (8, Read8),
    (9, Read9)
);
impl_attribute_reads!(
    (0, Read0),
    (1, Read1),
    (2, Read2),
    (3, Read3),
    (4, Read4),
    (5, Read5),
    (6, Read6),
    (7, Read7),
    (8, Read8),
    (9, Read9),
    (10, Read10)
);
impl_attribute_reads!(
    (0, Read0),
    (1, Read1),
    (2, Read2),
    (3, Read3),
    (4, Read4),
    (5, Read5),
    (6, Read6),
    (7, Read7),
    (8, Read8),
    (9, Read9),
    (10, Read10),
    (11, Read11)
);

impl Client {
    /// Reads committed Attribute values from the current or latest run of a Flow.
    ///
    /// Use this method to read Flow state from outside the Flow without defining an RPC.
    /// Pass a tuple of reads.
    /// Each read is an [`Attribute`] or an [`AttributeMapInstance`] from
    /// [`AttributeMap::instance`](crate::AttributeMap::instance), usually by reference.
    /// The method returns a tuple of the same arity.
    /// Each element is `Some(value)` when the value is set and `None` when it is not set.
    /// A set value that equals the type's default, such as an empty string or `0`, is still
    /// `Some`.
    /// Pass a one-element tuple, such as `(&ORDER_STATUS,)`, to read one value.
    /// A call accepts 1 to 12 reads.
    /// Read more values with several calls.
    /// Values from different calls can come from different commits.
    ///
    /// The Client sends every key in one server request.
    /// All values of one call therefore come from the same commit.
    /// No Worker runs, so the call works while no Worker is available.
    /// The call reads the current run, or the latest run when no run is open.
    /// A closed run stays readable until it is purged.
    ///
    /// The call sees only committed state.
    /// It does not see writes that a running RPC or Step has staged but not yet committed.
    /// An RPC handler that reads through its Context, in contrast, also sees its own staged
    /// writes.
    ///
    /// Hard-delete Attributes and AttributeMaps, defined with
    /// [`Attribute::hard_delete_on_null`], are readable.
    /// Dex resolves their stored values before it responds.
    /// A deleted hard-delete value reads as `None`.
    /// The Client resolves large-value references through the shared
    /// [`BlobCache`](crate::BlobCache) and the server, as other Client reads do.
    /// It decodes every value before it returns.
    /// On any error the call returns no values.
    /// The method blocks the calling thread until the server responds and every value is
    /// decoded.
    ///
    /// Every read must match a definition declared by some Flow in the Client's
    /// [`Registry`](crate::Registry).
    /// A definition matches when it has the same name, the same kind (Attribute or
    /// AttributeMap), the same Rust value type, and the same index, `sync_to_attribute_store`,
    /// and `hard_delete_on_null` settings.
    /// Channels and Streams with the same name are ignored.
    /// The server does not check that the target Flow's type declares the definition.
    /// A same-named Attribute of another type in the target Flow fails to decode.
    ///
    /// # Examples
    ///
    /// ```no_run
    /// use dex_sdk::{Attribute, AttributeMap, Client, SdkError};
    /// use std::sync::LazyLock;
    ///
    /// #[derive(serde::Deserialize, serde::Serialize)]
    /// struct Note {
    ///     text: String,
    /// }
    ///
    /// static ORDER_STATUS: LazyLock<Attribute<String>> =
    ///     LazyLock::new(|| Attribute::new("order-status"));
    /// static NOTES: LazyLock<AttributeMap<Note>> = LazyLock::new(|| AttributeMap::new("notes"));
    ///
    /// fn describe_order(client: &Client, order_id: &str) -> Result<String, SdkError> {
    ///     let (status, note) =
    ///         match client.get_attributes(order_id, (&ORDER_STATUS, &NOTES.instance("n1"))) {
    ///             Ok(values) => values,
    ///             Err(SdkError::FlowNotFound { .. }) => return Ok(format!("{order_id} does not exist")),
    ///             Err(error) => return Err(error),
    ///         };
    ///     let status = status.unwrap_or_else(|| "new".to_string());
    ///     let note = note.map_or_else(|| "no note yet".to_string(), |note| note.text);
    ///     Ok(format!("{order_id}: {status} ({note})"))
    /// }
    /// ```
    ///
    /// # Parameters
    ///
    /// - `flow_id` is the Flow ID. It must be non-blank.
    /// - `reads` is a tuple of 1 to 12 [`AttributeRead`] values. Two reads of the same Attribute
    ///   or AttributeMap instance are rejected.
    ///
    /// Returns one `Option<T>` per read, in read order, where `T` is the read's value type.
    ///
    /// # Errors
    ///
    /// Every error message except the one for a blank Flow ID names the Flow ID.
    /// An error about one read or value also names its Attribute name or physical key.
    /// Service-backed errors report `get_attributes` as their
    /// [`ServiceError::operation`](crate::ServiceError::operation).
    ///
    /// - [`SdkError::InvalidArgument`] for an empty or whitespace-only Flow ID, an empty map
    ///   instance key, a map instance key that contains `/`, or a duplicate read. The Client does
    ///   not contact Dex.
    /// - [`SdkError::FlowDefinition`] when no Flow in the Registry declares a matching definition.
    ///   The Client does not contact Dex.
    /// - [`SdkError::FlowNotFound`] when the Flow does not exist or its latest run is being purged
    ///   when the values are read.
    /// - [`SdkError::ValueMapping`] when a stored value cannot be decoded into its value type, or
    ///   a large-value reference cannot be resolved through the blob cache. A decode error gives
    ///   the target type and a reason, such as a missing field name, but never quotes the value.
    ///   A purge of the run, or a concurrent overwrite of a large value, between the read and
    ///   the large-value resolution also returns this error. Retry the call to read fresh state.
    /// - [`SdkError::Service`] for an invalid server response, a failed large-value request, or
    ///   another server or transport failure. Other service-backed variants are possible when
    ///   Dex reports a more specific status.
    pub fn get_attributes<Reads: AttributeReads>(
        &self,
        flow_id: &str,
        reads: Reads,
    ) -> SdkResult<Reads::Values> {
        let keys = self.validate_attribute_reads(flow_id, reads.attribute_read_targets())?;
        let values = self.fetch_attribute_values(flow_id, &keys)?;
        let mut decoder = sealed::AttributeValueDecoder {
            flow_id: flow_id.to_string(),
            values: keys.into_iter().zip(values).collect::<Vec<_>>().into_iter(),
        };
        Reads::decode_attribute_values(&mut decoder)
    }

    /// Returns the physical key of each read in read order, or the first invalid read's error.
    fn validate_attribute_reads(
        &self,
        flow_id: &str,
        targets: Vec<sealed::AttributeReadTarget>,
    ) -> SdkResult<Vec<String>> {
        if flow_id.trim().is_empty() {
            return Err(invalid("get_attributes requires a non-blank Flow ID"));
        }
        let mut keys = Vec::with_capacity(targets.len());
        let mut seen_keys = HashSet::with_capacity(targets.len());
        for target in targets {
            let name = &target.definition.name;
            let read_context = || format!("get_attributes Flow {flow_id:?} Attribute {name:?}");
            self.registry
                .require_registered_attribute(&target.definition)
                .map_err(|error| {
                    error.with_call_context(GET_ATTRIBUTES_OPERATION, flow_id, read_context())
                })?;
            let key = match &target.instance {
                Some(instance) => map_physical_name(name, instance).map_err(|error| {
                    error.with_call_context(GET_ATTRIBUTES_OPERATION, flow_id, read_context())
                })?,
                None => name.clone(),
            };
            if !seen_keys.insert(key.clone()) {
                return Err(invalid(format!(
                    "get_attributes Flow {flow_id:?} has duplicate Attribute read {key:?}"
                )));
            }
            keys.push(key);
        }
        Ok(keys)
    }

    /// Returns one hydrated value slot per key, in key order; `None` means the key is not set.
    fn fetch_attribute_values(
        &self,
        flow_id: &str,
        keys: &[String],
    ) -> SdkResult<Vec<Option<ProtoValue>>> {
        let call_context = format!("get_attributes Flow {flow_id:?} Attributes {keys:?}");
        let with_context = |error: SdkError| {
            error.with_call_context(GET_ATTRIBUTES_OPERATION, flow_id, call_context.clone())
        };
        let mut service = self.service.clone();
        let response = self
            .runtime
            .block_on(service.get_attributes(GetAttributesRequest {
                flow_id: flow_id.to_string(),
                run_id: String::new(),
                keys: keys.to_vec(),
                all_keys: false,
            }))
            .map_err(|status| {
                with_context(SdkError::from_status(
                    status,
                    GET_ATTRIBUTES_OPERATION,
                    Some(flow_id),
                    FlowTargetRequirement::Existing,
                ))
            })?
            .into_inner();
        let response_values =
            index_response_values(keys, response.attributes).map_err(with_context)?;
        let (set_keys, set_values): (Vec<String>, Vec<ProtoValue>) =
            response_values.into_iter().unzip();
        let hydrated_values = self
            .runtime
            .block_on(self.hydrator.hydrate_all(flow_id, set_values))
            .map_err(|error| {
                error
                    .with_flow_target_requirement(FlowTargetRequirement::Existing)
                    .with_call_context(
                        GET_ATTRIBUTES_OPERATION,
                        flow_id,
                        format!("{call_context} LoadBlobs"),
                    )
            })?;
        let mut hydrated_values_by_key = set_keys
            .into_iter()
            .zip(hydrated_values)
            .collect::<HashMap<_, _>>();
        Ok(keys
            .iter()
            .map(|key| hydrated_values_by_key.remove(key))
            .collect())
    }
}

/// Returns the value of every set key, or an error for an unrequested, repeated, or valueless
/// response entry.
fn index_response_values(
    keys: &[String],
    attributes: Vec<Kv>,
) -> SdkResult<HashMap<String, ProtoValue>> {
    let requested_keys = keys.iter().map(String::as_str).collect::<HashSet<_>>();
    let mut response_values = HashMap::with_capacity(attributes.len());
    for attribute in attributes {
        let key = attribute.key;
        let response_error = |problem: &str| SdkError::Service {
            service: ServiceError::local(
                GET_ATTRIBUTES_OPERATION,
                format!("GetAttributes returned {problem} {key:?}"),
            ),
        };
        if !requested_keys.contains(key.as_str()) {
            return Err(response_error("unexpected Attribute"));
        }
        if response_values.contains_key(&key) {
            return Err(response_error("duplicate Attribute"));
        }
        let Some(value) = attribute.value else {
            return Err(response_error("no value for Attribute"));
        };
        response_values.insert(key, value);
    }
    Ok(response_values)
}

#[cfg(test)]
mod tests {
    use dex_protocol::dex::{EncodedObject, Kv, Value as ProtoValue, value};

    use super::index_response_values;
    use super::sealed::AttributeValueDecoder;
    use crate::SdkError;

    #[derive(Debug, serde::Deserialize, serde::Serialize)]
    #[serde(deny_unknown_fields)]
    struct Credential {
        token: String,
    }

    #[test]
    fn decode_errors_keep_value_free_detail_and_omit_the_stored_value() {
        let mut decoder = decoder(vec![
            string_value("sekrit"),
            json_value(br#"{}"#),
            json_value(br#"{"token":"sekrit","extra":"sekrit"}"#),
            json_value(br#"{"token":"sekrit""#),
        ]);

        let invalid_type = decode_error(decoder.decode_next::<i64>());
        assert!(invalid_type.contains("\"order-1\""), "{invalid_type}");
        assert!(invalid_type.contains("\"key-0\""), "{invalid_type}");
        assert!(invalid_type.contains("i64"), "{invalid_type}");
        assert!(invalid_type.contains("data error"), "{invalid_type}");

        let missing_field = decode_error(decoder.decode_next::<Credential>());
        assert!(missing_field.contains("\"key-1\""), "{missing_field}");
        assert!(
            missing_field.contains("missing field `token`"),
            "{missing_field}"
        );

        let unknown_field = decode_error(decoder.decode_next::<Credential>());
        assert!(
            unknown_field.contains("data error: unknown field"),
            "{unknown_field}"
        );
        assert!(!unknown_field.contains("expected"), "{unknown_field}");
        assert!(!unknown_field.contains("extra"), "{unknown_field}");

        let syntax = decode_error(decoder.decode_next::<Credential>());
        assert!(syntax.contains("unexpected end of input"), "{syntax}");
        assert!(syntax.contains("at line 1 column"), "{syntax}");

        for message in [invalid_type, missing_field, unknown_field, syntax] {
            assert!(!message.contains("sekrit"), "{message}");
        }
    }

    #[test]
    fn response_values_are_indexed_by_requested_key() {
        let keys = vec!["status".to_string(), "notes/n1".to_string()];

        let values = index_response_values(&keys, vec![kv("notes/n1", Some(string_value("note")))])
            .expect("a subset of the requested keys is valid");

        assert_eq!(1, values.len());
        assert_eq!(Some(&string_value("note")), values.get("notes/n1"));
    }

    #[test]
    fn response_values_reject_unexpected_duplicate_and_valueless_keys() {
        let keys = vec!["status".to_string()];
        for (attributes, expected) in [
            (
                vec![kv("other", Some(string_value("x")))],
                "GetAttributes returned unexpected Attribute \"other\"",
            ),
            (
                vec![
                    kv("status", Some(string_value("x"))),
                    kv("status", Some(string_value("y"))),
                ],
                "GetAttributes returned duplicate Attribute \"status\"",
            ),
            (
                vec![kv("status", None)],
                "GetAttributes returned no value for Attribute \"status\"",
            ),
        ] {
            let error = index_response_values(&keys, attributes)
                .expect_err("an invalid response is rejected");
            assert!(matches!(error, SdkError::Service { .. }));
            assert_eq!(expected, error.to_string());
        }
    }

    fn decoder(values: Vec<ProtoValue>) -> AttributeValueDecoder {
        AttributeValueDecoder {
            flow_id: "order-1".to_string(),
            values: values
                .into_iter()
                .enumerate()
                .map(|(index, value)| (format!("key-{index}"), Some(value)))
                .collect::<Vec<_>>()
                .into_iter(),
        }
    }

    fn decode_error<T: std::fmt::Debug>(result: crate::SdkResult<Option<T>>) -> String {
        let error = result.expect_err("the value does not decode");
        assert!(matches!(error, SdkError::ValueMapping { .. }), "{error:?}");
        error.to_string()
    }

    fn kv(key: &str, value: Option<ProtoValue>) -> Kv {
        Kv {
            key: key.to_string(),
            value,
        }
    }

    fn string_value(text: &str) -> ProtoValue {
        ProtoValue {
            kind: Some(value::Kind::StringValue(text.to_string())),
        }
    }

    fn json_value(payload: &[u8]) -> ProtoValue {
        ProtoValue {
            kind: Some(value::Kind::ObjValue(EncodedObject {
                encoding: "json".to_string(),
                payload: payload.to_vec(),
            })),
        }
    }
}
