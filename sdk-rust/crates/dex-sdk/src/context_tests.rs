// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::{Context, ContextInput, InvocationCancellation, InvocationMethod};
use crate::server_protocol::NegotiatedServerProtocolVersion;
use crate::{
    Attribute, AttributeMap, Channel, ChannelMap, Flow, PersistenceSchema, Registry,
    registry::physical_name, value_mapper,
};
use dex_protocol::dex::{
    ChannelInfo, ChannelMessage as ProtoChannelMessage, ChannelValues, Context as ProtoContext, Kv,
};
use std::collections::HashMap;

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
    let special = "special % key";
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
            loaded_channel_messages: HashMap::new(),
            loaded_attribute_map_instances: vec!["items/".to_string()],
            loaded_channel_names: Vec::new(),
            loaded_channel_map_instances: Vec::new(),
            negotiated_protocol_version: NegotiatedServerProtocolVersion::default(),
        },
        None,
        InvocationCancellation::new(),
    )
    .expect("create RPC Context");

    assert!(
        attributes
            .set(&mut context, "tenant/order", "invalid".to_string())
            .is_err()
    );
    assert!(
        channels
            .publish(&mut context, "tenant/order", "invalid".to_string())
            .is_err()
    );

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
    assert!(context.record_heartbeat().is_err());
    assert!(
        context
            .record_heartbeat_value("checkpoint".to_string())
            .is_err()
    );
    assert!(context.last_heartbeat_value::<String>().is_err());
}

struct SelectiveFlow {
    attributes: AttributeMap<String>,
    queued: Channel<String>,
    by_tenant: ChannelMap<String>,
}

impl Flow for SelectiveFlow {
    type StartInput = ();

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute_map(&self.attributes)
            .channel(&self.queued)
            .channel_map(&self.by_tenant)
    }
}

#[test]
fn rpc_selective_state_snapshots_are_typed_and_presence_aware() {
    let flow = SelectiveFlow {
        attributes: AttributeMap::new("selected-items"),
        queued: Channel::new("selected-commands"),
        by_tenant: ChannelMap::new("selected-by-tenant"),
    };
    let registry = Registry::new()
        .register(flow)
        .expect("register selective Flow");
    let registered = registry
        .flow("SelectiveFlow")
        .expect("lookup selective Flow")
        .clone();
    let attributes = AttributeMap::<String>::new("selected-items");
    let queued = Channel::<String>::new("selected-commands");
    let by_tenant = ChannelMap::<String>::new("selected-by-tenant");
    let attribute_name = physical_name("selected-items", "tenant-a");
    let channel_map_name = physical_name("selected-by-tenant", "tenant-a");
    let mut context = Context::new(
        ContextInput {
            method: InvocationMethod::Rpc,
            flow: registered,
            metadata: ProtoContext::default(),
            attributes: vec![Kv {
                key: attribute_name.clone(),
                value: Some(value_mapper::encode_handler(&"value".to_string()).unwrap()),
            }],
            locals: Vec::new(),
            condition_results: None,
            channel_infos: HashMap::new(),
            loaded_channel_messages: HashMap::from([
                (
                    "selected-commands".to_string(),
                    ChannelValues {
                        messages: vec![ProtoChannelMessage {
                            channel_name: "selected-commands".to_string(),
                            value: Some(
                                value_mapper::encode_handler(&"first".to_string()).unwrap(),
                            ),
                            message_id: "message-1".to_string(),
                        }],
                    },
                ),
                (
                    channel_map_name.clone(),
                    ChannelValues {
                        messages: vec![ProtoChannelMessage {
                            channel_name: channel_map_name.clone(),
                            value: Some(
                                value_mapper::encode_handler(&"second".to_string()).unwrap(),
                            ),
                            message_id: "message-2".to_string(),
                        }],
                    },
                ),
            ]),
            loaded_attribute_map_instances: vec![attribute_name],
            loaded_channel_names: vec!["selected-commands".to_string()],
            loaded_channel_map_instances: vec![channel_map_name],
            negotiated_protocol_version: NegotiatedServerProtocolVersion::default(),
        },
        None,
        InvocationCancellation::new(),
    )
    .expect("create selective RPC Context");

    assert_eq!(
        Some("value".to_string()),
        attributes.get(&context, "tenant-a").unwrap()
    );
    assert_eq!("first", queued.pending_messages(&context).unwrap()[0].value);
    assert_eq!(
        "message-2",
        by_tenant.pending_messages(&context, "tenant-a").unwrap()[0].message_id
    );
    assert_eq!(
        Some("first".to_string()),
        queued
            .find_pending_message(&context, "message-1")
            .unwrap()
            .map(|message| message.value)
    );
    assert!(attributes.get(&context, "other").is_err());
    assert!(by_tenant.pending_messages(&context, "other").is_err());
    assert!(queued.publish(&mut context, "later".to_string()).is_ok());
    assert_eq!(1, queued.pending_messages(&context).unwrap().len());
}

struct HardDeleteFlow;

#[derive(serde::Deserialize, serde::Serialize)]
struct NumericNewtype(u64);

static API_KEY: std::sync::LazyLock<Attribute<String>> =
    std::sync::LazyLock::new(|| Attribute::new("api-key").hard_delete_on_null());
static NUMERIC_SECRET: std::sync::LazyLock<Attribute<NumericNewtype>> =
    std::sync::LazyLock::new(|| Attribute::new("numeric-secret").hard_delete_on_null());
static TENANT_KEYS: std::sync::LazyLock<AttributeMap<String>> =
    std::sync::LazyLock::new(|| AttributeMap::new("tenant-keys").hard_delete_on_null());
static PLAIN_API_KEY: std::sync::LazyLock<Attribute<String>> =
    std::sync::LazyLock::new(|| Attribute::new("api-key"));

impl Flow for HardDeleteFlow {
    type StartInput = ();

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&API_KEY)
            .attribute(&NUMERIC_SECRET)
            .attribute_map(&TENANT_KEYS)
    }
}

fn hard_delete_context(negotiated_protocol_version: u32) -> Context {
    let registry = Registry::new()
        .register(HardDeleteFlow)
        .expect("register hard-delete Flow");
    let registered = registry
        .flow("HardDeleteFlow")
        .expect("lookup hard-delete Flow")
        .clone();
    let negotiated = NegotiatedServerProtocolVersion::default();
    negotiated.store(negotiated_protocol_version);
    Context::new(
        ContextInput {
            method: InvocationMethod::Rpc,
            flow: registered,
            metadata: ProtoContext::default(),
            attributes: Vec::new(),
            locals: Vec::new(),
            condition_results: None,
            channel_infos: HashMap::new(),
            loaded_channel_messages: HashMap::new(),
            loaded_attribute_map_instances: vec!["tenant-keys/".to_string()],
            loaded_channel_names: Vec::new(),
            loaded_channel_map_instances: Vec::new(),
            negotiated_protocol_version: negotiated,
        },
        None,
        InvocationCancellation::new(),
    )
    .expect("create hard-delete Context")
}

#[test]
fn hard_delete_writes_set_the_flag_and_validate_values_and_protocol() {
    let mut context = hard_delete_context(3);
    API_KEY.set(&mut context, "key-1".to_string()).unwrap();
    TENANT_KEYS
        .set(&mut context, "tenant-a", "key-a".to_string())
        .unwrap();
    TENANT_KEYS.delete(&mut context, "tenant-b").unwrap();
    let error = NUMERIC_SECRET
        .set(&mut context, NumericNewtype(7))
        .expect_err("numeric value");
    assert!(
        error
            .to_string()
            .contains("numeric and Boolean values are rejected")
    );
    NUMERIC_SECRET.delete(&mut context).unwrap();
    let error = PLAIN_API_KEY
        .set(&mut context, "leak".to_string())
        .expect_err("mismatched definition");
    assert!(
        error
            .to_string()
            .contains("does not match its registered definition")
    );
    assert_eq!(Some("key-1".to_string()), API_KEY.get(&context).unwrap());

    let mut writes = context.take_outputs().attributes;
    writes.sort_by(|left, right| left.key.cmp(&right.key));
    let keys = writes
        .iter()
        .map(|write| write.key.as_str())
        .collect::<Vec<_>>();
    assert_eq!(
        vec![
            "api-key",
            "numeric-secret",
            "tenant-keys/tenant-a",
            "tenant-keys/tenant-b"
        ],
        keys
    );
    assert!(writes.iter().all(|write| write.is_hard_delete_on_null
        && write.index_config.is_none()
        && write.sync_config.is_none()));

    for version in [0, 1, 2] {
        let mut context = hard_delete_context(version);
        let error = API_KEY
            .set(&mut context, "key-1".to_string())
            .expect_err("old Server");
        assert!(error.to_string().contains("protocol version 3 or later"));
        let error = API_KEY.delete(&mut context).expect_err("old Server");
        assert!(error.to_string().contains("protocol version 3 or later"));
        assert!(context.take_outputs().attributes.is_empty());
    }
}

#[test]
fn recovery_attempt_progress_maps_every_value_and_unknown_values_conservatively() {
    for (wire_value, expected) in [
        (0, crate::StepAttemptProgress::Unspecified),
        (1, crate::StepAttemptProgress::NotStarted),
        (2, crate::StepAttemptProgress::NotCompleted),
        (99, crate::StepAttemptProgress::Unspecified),
    ] {
        let registry = Registry::new().register(HardDeleteFlow).unwrap();
        let context = Context::new(
            ContextInput {
                method: InvocationMethod::Execute,
                flow: registry.flow("HardDeleteFlow").unwrap().clone(),
                metadata: ProtoContext {
                    recovery_error: Some(dex_protocol::dex::RecoveryErrorInfo {
                        detail: "failure detail".to_string(),
                        error_type: "failure type".to_string(),
                        attempt_progress: wire_value,
                    }),
                    ..Default::default()
                },
                attributes: Vec::new(),
                locals: Vec::new(),
                condition_results: None,
                channel_infos: HashMap::new(),
                loaded_channel_messages: HashMap::new(),
                loaded_attribute_map_instances: Vec::new(),
                loaded_channel_names: Vec::new(),
                loaded_channel_map_instances: Vec::new(),
                negotiated_protocol_version: NegotiatedServerProtocolVersion::default(),
            },
            None,
            InvocationCancellation::new(),
        )
        .unwrap();
        let recovery = context.recovery_error().unwrap();
        assert_eq!(recovery.attempt_progress(), expected);
        assert_eq!(recovery.detail(), "failure detail");
        assert_eq!(recovery.error_type(), "failure type");
    }
}
