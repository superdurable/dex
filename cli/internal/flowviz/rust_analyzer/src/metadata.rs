// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeSet;

use proc_macro2::Span;
use serde_json::{Value, json};
use syn::{Attribute, Expr, Fields, GenericArgument, Item, PathArguments, Type, spanned::Spanned};

use crate::expressions::{
    ExpressionCalls, arguments, associated_call, associated_name, expression_path, literal_string,
    peel, type_name,
};
use crate::{
    RustFlowGraph, RustFlowSource, WorkflowImplementation, associated_type, impl_method,
    source_span,
};

#[derive(Clone, Copy, PartialEq, Eq)]
pub(crate) enum ResourceKind {
    Attribute,
    AttributeMap,
    Channel,
    ChannelMap,
    Join,
    Stream,
}

impl ResourceKind {
    fn from_constructor(name: &str) -> Option<Self> {
        Some(match name {
            "Attribute" => Self::Attribute,
            "AttributeMap" => Self::AttributeMap,
            "Channel" => Self::Channel,
            "ChannelMap" => Self::ChannelMap,
            "Join" => Self::Join,
            "Stream" => Self::Stream,
            _ => return None,
        })
    }

    pub(crate) fn kind(self) -> &'static str {
        match self {
            Self::Attribute | Self::AttributeMap => "attribute",
            Self::Channel | Self::ChannelMap | Self::Join => "channel",
            Self::Stream => "stream",
        }
    }

    fn is_map(self) -> bool {
        matches!(self, Self::AttributeMap | Self::ChannelMap | Self::Join)
    }
}

pub(crate) struct PersistenceResource {
    pub(crate) id: String,
    pub(crate) key: String,
    pub(crate) kind: ResourceKind,
    value_type: String,
    index_key: Option<String>,
    index_type: Option<&'static str>,
    indexed: bool,
    location: Span,
}

impl<'source> RustFlowSource<'source> {
    pub(crate) fn index_resource(
        &mut self,
        symbol: &str,
        declaration: &Type,
        initializer: &'source Expr,
        location: Span,
    ) {
        let calls = ExpressionCalls::from_expression(initializer);
        if calls
            .calls
            .iter()
            .any(|call| associated_call(call, "Rpc", "new"))
        {
            self.rpc_definitions.insert(symbol.to_owned(), initializer);
            return;
        }
        let Some((constructor, kind)) = calls.calls.iter().find_map(|expression| {
            let (owner, method) = associated_name(expression)?;
            (method == "new").then_some(())?;
            Some((*expression, ResourceKind::from_constructor(&owner)?))
        }) else {
            return;
        };
        let Some(key) = arguments(constructor)
            .and_then(|args| args.first())
            .and_then(literal_string)
        else {
            return;
        };
        let resource_type = if type_name(declaration).as_deref() == Some("LazyLock") {
            generic_types(declaration)
                .first()
                .copied()
                .unwrap_or(declaration)
        } else {
            declaration
        };
        let value_type = generic_types(resource_type)
            .first()
            .map_or_else(String::new, |value| match type_name(value).as_deref() {
                Some("String" | "str") => "string".to_owned(),
                Some("Vec")
                    if generic_types(value)
                        .first()
                        .and_then(|item| type_name(item))
                        .as_deref()
                        == Some("String") =>
                {
                    "string-array".to_owned()
                }
                Some("bool") => "bool".to_owned(),
                Some("DateTime" | "OffsetDateTime" | "SystemTime") => "datetime".to_owned(),
                Some("f32" | "f64") => "double".to_owned(),
                Some(name) if integer_bounds(name).is_some() => "int64".to_owned(),
                _ => self.source_text(value.span()),
            });
        let index = calls.calls.iter().find_map(|expression| {
            let Expr::MethodCall(call) = expression else {
                return None;
            };
            (call.method == "indexed")
                .then(|| call.args.first())
                .flatten()
        });
        let index_calls = index.map(ExpressionCalls::from_expression);
        let index_type = index_calls.as_ref().and_then(|calls| {
            calls.calls.iter().find_map(|expression| {
                let (owner, method) = associated_name(expression)?;
                if owner != "AttributeIndex" {
                    return None;
                }
                Some(match method.as_str() {
                    "keyword" => "keyword",
                    "full_text" => "fulltext",
                    "keyword_array" => "keyword-array",
                    "int" => "int",
                    "double" => "double",
                    "bool" => "bool",
                    "date_time" => "datetime",
                    _ => return None,
                })
            })
        });
        let index_key = index_calls
            .as_ref()
            .and_then(|calls| {
                calls.calls.iter().find_map(|expression| {
                    let Expr::MethodCall(call) = expression else {
                        return None;
                    };
                    (call.method == "with_key").then_some(())?;
                    call.args.first().and_then(literal_string)
                })
            })
            .or_else(|| index.map(|_| key.clone()));
        let id = format!("resource:{}:{key}", kind.kind());
        self.resources.insert(
            symbol.to_owned(),
            PersistenceResource {
                id,
                key,
                kind,
                value_type,
                index_key,
                index_type,
                indexed: index.is_some(),
                location,
            },
        );
    }

    pub(crate) fn add_persistence(
        &self,
        flow: &WorkflowImplementation<'source>,
        graph: &mut RustFlowGraph,
    ) {
        for resource in self.resources.values() {
            graph.node(
                resource.id.clone(),
                resource.kind.kind(),
                &resource.key,
                resource.location,
                json!({
                    "resource": {"valueType": resource.value_type, "map": resource.kind.is_map()}
                }),
            );
            if resource.kind.kind() == "attribute" {
                graph.facts("attributes").push(json!({
                    "resourceId": resource.id, "key": resource.key, "valueType": resource.value_type,
                    "isMap": resource.kind.is_map(), "isIndexed": resource.indexed,
                    "indexKey": resource.index_key, "indexType": resource.index_type,
                    "directives": self.directive_facts(resource.location, "dex:indexed-attribute"),
                    "span": source_span(resource.location)
                }));
            }
        }
        if self.flows.len() < 2 {
            return;
        }
        let mut registered = BTreeSet::new();
        if let Some(method) = impl_method(flow.declaration, "persistence") {
            let expressions = ExpressionCalls::new(&method.block);
            for expression in &expressions.calls {
                let Expr::MethodCall(call) = expression else {
                    continue;
                };
                if matches!(
                    call.method.to_string().as_str(),
                    "attribute" | "attribute_map" | "channel" | "channel_map" | "join" | "stream"
                ) && let Some(resource) = call
                    .args
                    .first()
                    .and_then(|argument| self.resource(argument, &expressions, 0))
                {
                    registered.insert(&resource.id);
                }
            }
        }
        graph.analysis["persistenceResourceIds"] = json!(registered);
    }

    pub(crate) fn resource(
        &self,
        expression: &Expr,
        calls: &ExpressionCalls<'_>,
        depth: usize,
    ) -> Option<&PersistenceResource> {
        if depth >= 16 {
            return None;
        }
        match peel(expression) {
            Expr::Path(_) => {
                let name = expression_path(expression)?;
                if let Some(value) = calls.locals.get(&name) {
                    return self.resource(value, calls, depth + 1);
                }
                self.resources.get(&name)
            }
            Expr::Unary(unary) if matches!(unary.op, syn::UnOp::Deref(_)) => {
                self.resource(&unary.expr, calls, depth + 1)
            }
            Expr::MethodCall(call)
                if matches!(
                    call.method.to_string().as_str(),
                    "channel_map"
                        | "instance"
                        | "when_empty"
                        | "lock"
                        | "load"
                        | "load_messages"
                        | "clone"
                ) =>
            {
                self.resource(&call.receiver, calls, depth + 1)
            }
            _ => None,
        }
    }

    pub(crate) fn start_input(
        &self,
        flow: &WorkflowImplementation<'source>,
        step: &WorkflowImplementation<'source>,
        graph: &mut RustFlowGraph,
    ) {
        let location = step.declaration.span();
        let input = associated_type(step.declaration, "Input")
            .or_else(|| associated_type(flow.declaration, "StartInput"));
        let result = input
            .ok_or_else(|| "Start Step has no input type".to_owned())
            .and_then(|input| self.input_schema(input, 0, &mut BTreeSet::new()));
        let fact = match result {
            Ok(schema) => {
                json!({"stepType": step.name, "input": schema, "span": source_span(location)})
            }
            Err(error) => {
                graph.diagnostic(
                    "warning",
                    "start_input_unresolved",
                    error.clone(),
                    Some(location),
                );
                json!({"stepType": step.name, "error": error, "span": source_span(location)})
            }
        };
        graph.analysis["startInput"] = fact;
    }

    fn input_schema(
        &self,
        input: &Type,
        depth: usize,
        ancestors: &mut BTreeSet<String>,
    ) -> Result<Value, String> {
        if depth > 32 {
            return Err("nesting exceeds 32 levels".to_owned());
        }
        if let Type::Reference(reference) = input {
            return self.input_schema(&reference.elem, depth + 1, ancestors);
        }
        if let Type::Array(array) = input {
            let Expr::Lit(length) = &array.len else {
                return Err("array length must be static".to_owned());
            };
            let syn::Lit::Int(length) = &length.lit else {
                return Err("array length must be an integer".to_owned());
            };
            return Ok(
                json!({"kind": "array", "fixedLength": length.base10_parse::<i64>().map_err(|error| error.to_string())?,
                "items": self.input_schema(&array.elem, depth + 1, ancestors)?}),
            );
        }
        if let Type::Tuple(tuple) = input
            && tuple.elems.is_empty()
        {
            return Ok(json!({"kind": "null"}));
        }
        let name = type_name(input)
            .ok_or_else(|| format!("unsupported type {}", self.source_text(input.span())))?;
        if let Some((minimum, maximum)) = integer_bounds(&name) {
            return Ok(json!({"kind": "integer", "minimum": minimum, "maximum": maximum}));
        }
        let generic = generic_types(input);
        match (name.as_str(), generic.as_slice()) {
            ("String" | "str" | "char", _) => Ok(json!({"kind": "string"})),
            ("bool", _) => Ok(json!({"kind": "boolean"})),
            ("f32" | "f64", _) => Ok(json!({"kind": "number"})),
            ("DateTime" | "OffsetDateTime" | "SystemTime", _) => {
                Ok(json!({"kind": "string", "format": "date-time"}))
            }
            ("Option", [value]) => {
                let mut schema = self.input_schema(value, depth + 1, ancestors)?;
                if schema["kind"] != "null" {
                    schema["nullable"] = json!(true);
                }
                Ok(schema)
            }
            ("Box", [value]) => self.input_schema(value, depth + 1, ancestors),
            ("Vec", [value]) => Ok(
                json!({"kind": "array", "items": self.input_schema(value, depth + 1, ancestors)?}),
            ),
            ("HashMap" | "BTreeMap", [key, value])
                if type_name(key).as_deref() == Some("String") =>
            {
                Ok(
                    json!({"kind": "map", "values": self.input_schema(value, depth + 1, ancestors)?}),
                )
            }
            _ => {
                if !ancestors.insert(name.clone()) {
                    return Err(format!("type {name} is recursive"));
                }
                let result = self.declared_input_schema(&name, depth, ancestors);
                ancestors.remove(&name);
                result
            }
        }
    }

    fn declared_input_schema(
        &self,
        name: &str,
        depth: usize,
        ancestors: &mut BTreeSet<String>,
    ) -> Result<Value, String> {
        match self.declarations.get(name) {
            Some(Item::Type(alias)) => self.input_schema(&alias.ty, depth + 1, ancestors),
            Some(Item::Struct(declaration)) => self.struct_schema(declaration, depth, ancestors),
            Some(Item::Enum(declaration)) => {
                let serde = serde_settings(&declaration.attrs)?;
                let mut values = Vec::new();
                for variant in &declaration.variants {
                    let variant_serde = serde_settings(&variant.attrs)?;
                    if variant_serde.skip {
                        continue;
                    }
                    if !matches!(variant.fields, Fields::Unit) {
                        return Err(format!("enum {name} contains a non-unit variant"));
                    }
                    let name = variant.ident.to_string();
                    let value = variant_serde
                        .rename
                        .unwrap_or_else(|| rename_serde(&name, serde.rename_all.as_deref(), true));
                    values.push(json!({"name": name, "value": value}));
                }
                Ok(json!({"kind": "string", "enumValues": values}))
            }
            _ => Err(format!(
                "type {name} is unsupported or is not declared in this file"
            )),
        }
    }

    fn struct_schema(
        &self,
        declaration: &syn::ItemStruct,
        depth: usize,
        ancestors: &mut BTreeSet<String>,
    ) -> Result<Value, String> {
        let serde = serde_settings(&declaration.attrs)?;
        let Fields::Named(fields) = &declaration.fields else {
            return Err(format!(
                "struct {} must have named fields",
                declaration.ident
            ));
        };
        let mut schema_fields = Vec::new();
        for field in &fields.named {
            let field_serde = serde_settings(&field.attrs)?;
            if field_serde.skip {
                continue;
            }
            let field_name = field
                .ident
                .as_ref()
                .expect("named field has an identifier")
                .to_string();
            let name = field_serde.rename.unwrap_or_else(|| {
                rename_serde(
                    field_name.trim_start_matches("r#"),
                    serde.rename_all.as_deref(),
                    false,
                )
            });
            let required = !serde.default
                && !field_serde.default
                && type_name(&field.ty).as_deref() != Some("Option");
            let schema = self.input_schema(&field.ty, depth + 1, ancestors)?;
            schema_fields.push(json!({"name": name, "required": required, "schema": schema}));
        }
        Ok(json!({"kind": "object", "fields": schema_fields}))
    }
}

fn generic_types(value: &Type) -> Vec<&Type> {
    let Type::Path(path) = value else {
        return Vec::new();
    };
    let Some(segment) = path.path.segments.last() else {
        return Vec::new();
    };
    let PathArguments::AngleBracketed(arguments) = &segment.arguments else {
        return Vec::new();
    };
    arguments
        .args
        .iter()
        .filter_map(|argument| match argument {
            GenericArgument::Type(value) => Some(value),
            _ => None,
        })
        .collect()
}

fn integer_bounds(name: &str) -> Option<(String, String)> {
    let (minimum, maximum) = match name {
        "i8" => (i8::MIN as i128, i8::MAX as u128),
        "i16" => (i16::MIN as i128, i16::MAX as u128),
        "i32" => (i32::MIN as i128, i32::MAX as u128),
        "i64" | "isize" => (i64::MIN as i128, i64::MAX as u128),
        "i128" => (i128::MIN, i128::MAX as u128),
        "u8" => (0, u8::MAX as u128),
        "u16" => (0, u16::MAX as u128),
        "u32" => (0, u32::MAX as u128),
        "u64" | "usize" => (0, u64::MAX as u128),
        "u128" => (0, u128::MAX),
        _ => return None,
    };
    Some((minimum.to_string(), maximum.to_string()))
}

#[derive(Default)]
struct SerdeSettings {
    rename: Option<String>,
    rename_all: Option<String>,
    default: bool,
    skip: bool,
}

fn serde_settings(attributes: &[Attribute]) -> Result<SerdeSettings, String> {
    let mut settings = SerdeSettings::default();
    for attribute in attributes
        .iter()
        .filter(|attribute| attribute.path().is_ident("serde"))
    {
        attribute
            .parse_nested_meta(|meta| {
                if meta.path.is_ident("skip") || meta.path.is_ident("skip_deserializing") {
                    settings.skip = true;
                } else if meta.path.is_ident("default") {
                    settings.default = true;
                    if meta.input.peek(syn::Token![=]) {
                        let _: syn::LitStr = meta.value()?.parse()?;
                    }
                } else if meta.path.is_ident("rename") || meta.path.is_ident("rename_all") {
                    let rename = if meta.path.is_ident("rename") {
                        &mut settings.rename
                    } else {
                        &mut settings.rename_all
                    };
                    if meta.input.peek(syn::Token![=]) {
                        *rename = Some(meta.value()?.parse::<syn::LitStr>()?.value());
                    } else {
                        meta.parse_nested_meta(|direction| {
                            let value = direction.value()?.parse::<syn::LitStr>()?.value();
                            if direction.path.is_ident("deserialize") {
                                *rename = Some(value);
                            }
                            Ok(())
                        })?;
                    }
                } else if meta.path.is_ident("alias") || meta.path.is_ident("skip_serializing_if") {
                    let _: syn::LitStr = meta.value()?.parse()?;
                } else if !meta.path.is_ident("skip_serializing")
                    && !meta.path.is_ident("deny_unknown_fields")
                {
                    return Err(meta.error("unsupported serde setting on Start input"));
                }
                Ok(())
            })
            .map_err(|error| error.to_string())?;
    }
    Ok(settings)
}

fn rename_serde(name: &str, rule: Option<&str>, variant: bool) -> String {
    let mut words = Vec::new();
    if variant {
        let mut start = 0;
        for (index, character) in name.char_indices().skip(1) {
            if character.is_ascii_uppercase() {
                words.push(&name[start..index]);
                start = index;
            }
        }
        words.push(&name[start..]);
    } else {
        words.extend(name.split('_'));
    }
    match rule {
        Some("lowercase") if variant => name.to_ascii_lowercase(),
        Some("UPPERCASE") => name.to_ascii_uppercase(),
        Some("snake_case") if variant => words.join("_").to_ascii_lowercase(),
        Some("SCREAMING_SNAKE_CASE") => words.join("_").to_ascii_uppercase(),
        Some("kebab-case") => words.join("-").to_ascii_lowercase(),
        Some("SCREAMING-KEBAB-CASE") => words.join("-").to_ascii_uppercase(),
        Some("PascalCase" | "camelCase") if !variant => {
            let mut renamed = String::new();
            for (index, word) in words.into_iter().enumerate() {
                let mut characters = word.chars();
                if let Some(first) = characters.next() {
                    renamed.push(if index == 0 && rule == Some("camelCase") {
                        first
                    } else {
                        first.to_ascii_uppercase()
                    });
                    renamed.extend(characters);
                }
            }
            renamed
        }
        Some("camelCase") if variant => {
            let mut characters = name.chars();
            characters
                .next()
                .into_iter()
                .map(|first| first.to_ascii_lowercase())
                .chain(characters)
                .collect()
        }
        _ => name.to_owned(),
    }
}

pub(crate) fn rpc_output_keys(expressions: &ExpressionCalls<'_>) -> Option<Vec<String>> {
    let mut output = None;
    for expression in expressions.return_expressions() {
        let Expr::Call(ok) = expressions.resolve(expression) else {
            return None;
        };
        if expression_path(&ok.func).as_deref() != Some("Ok") {
            return None;
        }
        let result = expressions.resolve(ok.args.first()?);
        if !associated_call(result, "RpcResult", "new") {
            return None;
        }
        let value = expressions.resolve(arguments(result)?.first()?);
        let mut keys = if associated_name(value).is_some_and(|(owner, method)| {
            matches!(owner.as_str(), "HashMap" | "BTreeMap") && method == "from"
        }) {
            rpc_map_entries(arguments(value)?.first()?)?
                .into_iter()
                .map(|element| {
                    let Expr::Tuple(pair) = peel(element) else {
                        return None;
                    };
                    pair.elems.first().and_then(literal_string)
                })
                .collect::<Option<Vec<_>>>()?
        } else {
            return None;
        };
        keys.sort();
        if output.as_ref().is_some_and(|previous| previous != &keys) {
            return None;
        }
        output = Some(keys);
    }
    output
}

fn rpc_map_entries(expression: &Expr) -> Option<Vec<&Expr>> {
    let Expr::Array(array) = peel(expression) else {
        return None;
    };
    Some(array.elems.iter().collect())
}
