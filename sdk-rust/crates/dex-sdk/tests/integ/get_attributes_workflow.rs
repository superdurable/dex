// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, AttributeMap, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList,
    StartFlowOptions, StepList,
};

pub(crate) const INITIAL_LABEL: &str = "ready";
pub(crate) const INITIAL_COUNT: i64 = 7;
pub(crate) const SET_QUANTITY_INSTANCE: &str = "line 1";
pub(crate) const UNSET_QUANTITY_INSTANCE: &str = "line-2";

pub(crate) static LABEL: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("get-attributes-label"));
pub(crate) static EMPTY_LABEL: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("get-attributes-empty-label"));
pub(crate) static COUNT: LazyLock<Attribute<i64>> =
    LazyLock::new(|| Attribute::new("get-attributes-count"));
pub(crate) static ZERO_COUNT: LazyLock<Attribute<i64>> =
    LazyLock::new(|| Attribute::new("get-attributes-zero-count"));
pub(crate) static UNSET_COUNT: LazyLock<Attribute<i64>> =
    LazyLock::new(|| Attribute::new("get-attributes-unset-count"));
pub(crate) static NOTE: LazyLock<Attribute<GetAttributesNote>> =
    LazyLock::new(|| Attribute::new("get-attributes-note"));
pub(crate) static QUANTITIES: LazyLock<AttributeMap<i64>> =
    LazyLock::new(|| AttributeMap::new("get-attributes-quantities"));
pub(crate) static SECRET: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("get-attributes-secret").hard_delete_on_null());
pub(crate) static LARGE_VALUE: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("get-attributes-large-value"));
pub(crate) static CLOSED_RUN_LARGE_VALUE: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("get-attributes-closed-run-large-value"));
pub(crate) static LABEL_AS_NUMBER: LazyLock<Attribute<i64>> =
    LazyLock::new(|| Attribute::new("get-attributes-label"));

pub(crate) static INITIAL_NOTE: LazyLock<GetAttributesNote> = LazyLock::new(|| GetAttributesNote {
    text: "ship today".to_string(),
    priority: 2,
});
pub(crate) static SECRET_VALUE: LazyLock<String> =
    LazyLock::new(|| "hard-delete-value-".repeat(64));
pub(crate) static ROTATED_SECRET_VALUE: LazyLock<String> =
    LazyLock::new(|| "rotated-hard-delete-value-".repeat(64));
pub(crate) static LARGE_STRING_VALUE: LazyLock<String> =
    LazyLock::new(|| "large-value-".repeat(64));
pub(crate) static CLOSED_RUN_LARGE_STRING_VALUE: LazyLock<String> =
    LazyLock::new(|| "closed-run-large-value-".repeat(32));

#[derive(Clone, Debug, PartialEq, serde::Deserialize, serde::Serialize)]
pub(crate) struct GetAttributesNote {
    pub(crate) text: String,
    pub(crate) priority: i64,
}

pub(crate) struct GetAttributesFlow;

impl GetAttributesFlow {
    pub(crate) const ROTATE_SECRET: Rpc<String, ()> = Rpc::new("rotate_secret");
    pub(crate) const DELETE_SECRET: Rpc<(), ()> = Rpc::new("delete_secret");
    pub(crate) const DELETE_LABEL: Rpc<(), ()> = Rpc::new("delete_label");

    fn rotate_secret(&self, context: &mut Context, secret: String) -> HandlerResult<()> {
        SECRET.set(context, secret)
    }

    fn delete_secret(&self, context: &mut Context) -> HandlerResult<()> {
        SECRET.delete(context)
    }

    fn delete_label(&self, context: &mut Context) -> HandlerResult<()> {
        LABEL.delete(context)
    }
}

impl Flow for GetAttributesFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::empty()
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&LABEL)
            .attribute(&EMPTY_LABEL)
            .attribute(&COUNT)
            .attribute(&ZERO_COUNT)
            .attribute(&UNSET_COUNT)
            .attribute(&NOTE)
            .attribute_map(&QUANTITIES)
            .attribute(&SECRET)
            .attribute(&LARGE_VALUE)
            .attribute(&CLOSED_RUN_LARGE_VALUE)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure(Self::ROTATE_SECRET, Self::rotate_secret)
            .procedure_without_input(Self::DELETE_SECRET, Self::delete_secret)
            .procedure_without_input(Self::DELETE_LABEL, Self::delete_label)
    }
}

/// Declares the label Attribute name with a numeric value type for decode-mismatch reads.
pub(crate) struct LabelAsNumberFlow;

impl Flow for LabelAsNumberFlow {
    type StartInput = ();

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&LABEL_AS_NUMBER)
    }
}

pub(crate) fn get_attributes_start_options() -> StartFlowOptions {
    StartFlowOptions::new()
        .initial_attribute(&LABEL, INITIAL_LABEL.to_string())
        .initial_attribute(&EMPTY_LABEL, String::new())
        .initial_attribute(&COUNT, INITIAL_COUNT)
        .initial_attribute(&ZERO_COUNT, 0)
        .initial_attribute(&NOTE, INITIAL_NOTE.clone())
        .initial_attribute_map(&QUANTITIES, SET_QUANTITY_INSTANCE, 0)
        .initial_attribute(&SECRET, SECRET_VALUE.clone())
        .initial_attribute(&LARGE_VALUE, LARGE_STRING_VALUE.clone())
        .initial_attribute(
            &CLOSED_RUN_LARGE_VALUE,
            CLOSED_RUN_LARGE_STRING_VALUE.clone(),
        )
}
