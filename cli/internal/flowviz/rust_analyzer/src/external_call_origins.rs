// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use syn::{Block, Expr, Item};

use crate::expressions::{block_tail, peel};
use crate::external_call_targets::{Certainty, PathTarget, TypeScope, ValueOrigin};
use crate::external_calls::{BindingSource, DEX_TRAIT_DEFAULT_METHODS, MethodScan, path_segments};

/// Collection methods that return one element, wrapped in an Option.
const COLLECTION_ELEMENT_METHODS: &[&str] = &[
    "find",
    "first",
    "get",
    "get_mut",
    "last",
    "max",
    "min",
    "next",
    "nth",
    "peek",
    "pop",
    "pop_back",
    "pop_front",
];

/// Collection methods that return an iterator or collection of the same elements.
const COLLECTION_ITERATION_METHODS: &[&str] = &[
    "chain",
    "cloned",
    "copied",
    "drain",
    "filter",
    "into_iter",
    "iter",
    "iter_mut",
    "peekable",
    "rev",
    "skip",
    "take",
    "values",
    "values_mut",
];

/// Collection methods that return a count, a flag, or nothing.
const COLLECTION_PURE_RESULT_METHODS: &[&str] = &[
    "clear",
    "contains",
    "contains_key",
    "count",
    "dedup",
    "extend",
    "insert",
    "is_empty",
    "len",
    "push",
    "push_back",
    "retain",
    "sort",
    "truncate",
];

/// Option and Result methods that return the payload.
const OPTIONAL_UNWRAPPING_METHODS: &[&str] = &[
    "expect",
    "unwrap",
    "unwrap_or",
    "unwrap_or_default",
    "unwrap_or_else",
];

/// Option and Result methods that return an Option or Result of the same payload.
const OPTIONAL_PRESERVING_METHODS: &[&str] = &[
    "as_deref",
    "as_mut",
    "as_ref",
    "clone",
    "cloned",
    "copied",
    "filter",
    "inspect",
    "map_err",
    "ok",
    "ok_or",
    "ok_or_else",
    "or",
    "or_else",
    "take",
];

/// Option and Result methods that return a flag.
const OPTIONAL_FLAG_METHODS: &[&str] = &["is_err", "is_none", "is_ok", "is_some"];

impl<'ast> MethodScan<'_, '_, 'ast> {
    fn binding_origin(&self, name: &str, depth: usize) -> ValueOrigin {
        let Some(sources) = self.bindings.get(name) else {
            return ValueOrigin::Unknown;
        };
        let mut origins = sources.iter().map(|source| match source {
            BindingSource::Typed(declared) => {
                self.finder.source.type_origin(declared, &self.scope, 0)
            }
            BindingSource::Value(value) => self.expression_origin(value, depth + 1),
            BindingSource::Unwrapped(value) => self.expression_origin(value, depth + 1).unwrapped(),
            BindingSource::Element(value) => {
                element_origin(self.expression_origin(value, depth + 1))
            }
            BindingSource::Destructured(value) => match self.expression_origin(value, depth + 1) {
                external @ ValueOrigin::External(_) => external,
                _ => ValueOrigin::Unknown,
            },
            BindingSource::Untyped => ValueOrigin::Unknown,
        });
        let first = origins.next().unwrap_or(ValueOrigin::Unknown);
        origins.fold(first, ValueOrigin::merge)
    }

    pub(crate) fn expression_origin(&self, expression: &Expr, depth: usize) -> ValueOrigin {
        if depth >= 16 {
            return ValueOrigin::Unknown;
        }
        match expression {
            Expr::Try(attempt) => self.expression_origin(&attempt.expr, depth + 1).unwrapped(),
            Expr::Reference(reference) => self.expression_origin(&reference.expr, depth + 1),
            Expr::Paren(inner) => self.expression_origin(&inner.expr, depth + 1),
            Expr::Group(inner) => self.expression_origin(&inner.expr, depth + 1),
            Expr::Unary(unary) if matches!(unary.op, syn::UnOp::Deref(_)) => {
                self.expression_origin(&unary.expr, depth + 1)
            }
            Expr::Array(array) => ValueOrigin::collection(
                array
                    .elems
                    .iter()
                    .map(|element| self.expression_origin(element, depth + 1))
                    .reduce(ValueOrigin::merge)
                    .unwrap_or(ValueOrigin::PureStandardLibrary),
            ),
            Expr::Tuple(tuple) => ValueOrigin::container(
                tuple
                    .elems
                    .iter()
                    .map(|element| self.expression_origin(element, depth + 1)),
            ),
            Expr::Lit(_)
            | Expr::Binary(_)
            | Expr::Unary(_)
            | Expr::Range(_)
            | Expr::Cast(_)
            | Expr::Repeat(_)
            | Expr::Macro(_) => ValueOrigin::PureStandardLibrary,
            Expr::Path(value) if value.qself.is_none() => self.path_origin(&value.path, depth),
            Expr::Field(field) => self.field_origin(field, depth),
            Expr::Call(call) => self.call_origin(call, depth),
            Expr::MethodCall(call) => self.method_result_origin(call, depth),
            Expr::Struct(value) => self.path_origin(&value.path, depth),
            Expr::Index(index) => element_origin(self.expression_origin(&index.expr, depth + 1)),
            Expr::If(branch) => {
                let then_origin = self.block_origin(&branch.then_branch, depth);
                match &branch.else_branch {
                    Some((_, alternative)) => {
                        then_origin.merge(self.expression_origin(alternative, depth + 1))
                    }
                    None => then_origin,
                }
            }
            Expr::Match(branch) => branch
                .arms
                .iter()
                .map(|arm| self.expression_origin(&arm.body, depth + 1))
                .reduce(ValueOrigin::merge)
                .unwrap_or(ValueOrigin::Unknown),
            Expr::Block(block) => self.block_origin(&block.block, depth),
            Expr::Await(awaited) => self.expression_origin(&awaited.base, depth + 1),
            _ => ValueOrigin::Unknown,
        }
    }

    fn block_origin(&self, block: &Block, depth: usize) -> ValueOrigin {
        block_tail(block).map_or(ValueOrigin::Unknown, |tail| {
            self.expression_origin(tail, depth + 1)
        })
    }

    fn path_origin(&self, path: &syn::Path, depth: usize) -> ValueOrigin {
        let segments = path_segments(path);
        if let [name] = segments.as_slice() {
            if name == "self" {
                return self.owner_origin();
            }
            if self.is_binding(name) {
                return self.binding_origin(name, depth);
            }
            if self.finder.source.resources.contains_key(name) {
                return ValueOrigin::DexSdk;
            }
        }
        let source = self.finder.source;
        match source.path_target(&segments, &self.scope) {
            PathTarget::DexSdk => ValueOrigin::DexSdk,
            PathTarget::PureStandardLibrary => ValueOrigin::PureStandardLibrary,
            PathTarget::SelfType => self.owner_origin(),
            PathTarget::FileItem(name) => {
                if let Some((declared, _)) = source.constants.get(&name) {
                    return source.type_origin(declared, &self.scope, 0);
                }
                match source.declarations.get(&name) {
                    Some(Item::Struct(_) | Item::Enum(_)) => ValueOrigin::FileType(name),
                    _ => ValueOrigin::Unknown,
                }
            }
            PathTarget::DexSdkExternal | PathTarget::ExternalStandardLibrary => {
                ValueOrigin::External(Certainty::Definite)
            }
            PathTarget::External => ValueOrigin::External(Certainty::Possible),
        }
    }

    fn owner_origin(&self) -> ValueOrigin {
        if self.scope.owner.is_empty() {
            ValueOrigin::Unknown
        } else {
            ValueOrigin::FileType(self.scope.owner.clone())
        }
    }

    fn field_origin(&self, field: &syn::ExprField, depth: usize) -> ValueOrigin {
        let owner = match self.expression_origin(&field.base, depth + 1) {
            ValueOrigin::FileType(owner) => owner,
            external @ ValueOrigin::External(_) => return external,
            ValueOrigin::DexSdk => return ValueOrigin::DexSdk,
            ValueOrigin::PureStandardLibrary => return ValueOrigin::PureStandardLibrary,
            _ => return ValueOrigin::Unknown,
        };
        let Some(Item::Struct(declaration)) = self.finder.source.declarations.get(&owner) else {
            return ValueOrigin::Unknown;
        };
        let declared = match &field.member {
            syn::Member::Named(name) => declaration
                .fields
                .iter()
                .find(|candidate| candidate.ident.as_ref() == Some(name)),
            syn::Member::Unnamed(index) => declaration.fields.iter().nth(index.index as usize),
        };
        let Some(declared) = declared else {
            return ValueOrigin::Unknown;
        };
        let scope = TypeScope::new(&owner, [&declaration.generics]);
        self.finder.source.type_origin(&declared.ty, &scope, 0)
    }

    fn call_origin(&self, call: &syn::ExprCall, depth: usize) -> ValueOrigin {
        let Expr::Path(function) = peel(&call.func) else {
            return ValueOrigin::External(Certainty::Possible);
        };
        let segments = path_segments(&function.path);
        if let [name] = segments.as_slice()
            && self.is_binding(name)
        {
            return ValueOrigin::External(Certainty::Possible);
        }
        let last = segments.last().cloned().unwrap_or_default();
        let first_argument = || {
            call.args
                .first()
                .map_or(ValueOrigin::PureStandardLibrary, |argument| {
                    self.expression_origin(argument, depth + 1)
                })
        };
        let source = self.finder.source;
        match source.path_target(&segments, &self.scope) {
            PathTarget::DexSdk => ValueOrigin::DexSdk,
            PathTarget::PureStandardLibrary => match last.as_str() {
                "Some" | "Ok" | "Err" => ValueOrigin::Optional(Box::new(first_argument())),
                "new" | "from" | "clone"
                    if segments.len() >= 2
                        && matches!(
                            segments[segments.len() - 2].as_str(),
                            "Arc" | "Box" | "Clone" | "Rc"
                        ) =>
                {
                    first_argument()
                }
                // Other pure functions, such as serde_json::to_vec, return new standard values.
                _ => ValueOrigin::PureStandardLibrary,
            },
            PathTarget::FileItem(name) if name == last => match self.finder.file_function(&name) {
                Some(function) => self.finder.return_origin(function),
                None => ValueOrigin::FileType(name),
            },
            PathTarget::FileItem(owner) => self.associated_result_origin(&owner, &last),
            PathTarget::SelfType => self.associated_result_origin(&self.scope.owner, &last),
            PathTarget::DexSdkExternal | PathTarget::ExternalStandardLibrary => {
                ValueOrigin::External(Certainty::Definite)
            }
            PathTarget::External => ValueOrigin::External(Certainty::Possible),
        }
    }

    fn associated_result_origin(&self, owner: &str, function: &str) -> ValueOrigin {
        if function.starts_with(char::is_uppercase) {
            return ValueOrigin::FileType(owner.to_owned());
        }
        let methods = self.finder.file_methods(owner, function);
        if methods.is_empty() && self.finder.source.derives_method(owner, function) {
            return ValueOrigin::FileType(owner.to_owned());
        }
        if methods.is_empty() && DEX_TRAIT_DEFAULT_METHODS.contains(&function) {
            return ValueOrigin::PureStandardLibrary;
        }
        methods
            .into_iter()
            .map(|method| self.finder.return_origin(method))
            .reduce(ValueOrigin::merge)
            .unwrap_or(ValueOrigin::Unknown)
    }

    fn method_result_origin(&self, call: &syn::ExprMethodCall, depth: usize) -> ValueOrigin {
        let receiver = self.expression_origin(&call.receiver, depth + 1);
        let method = call.method.to_string();
        let method = method.as_str();
        match receiver {
            ValueOrigin::FileType(owner) => self.associated_result_origin(&owner, method),
            ValueOrigin::Collection(element) => {
                if COLLECTION_ELEMENT_METHODS.contains(&method) {
                    ValueOrigin::Optional(element)
                } else if COLLECTION_ITERATION_METHODS.contains(&method) || method == "clone" {
                    ValueOrigin::Collection(element)
                } else if COLLECTION_PURE_RESULT_METHODS.contains(&method) {
                    ValueOrigin::PureStandardLibrary
                } else {
                    ValueOrigin::Unknown
                }
            }
            ValueOrigin::Optional(payload) => {
                if OPTIONAL_UNWRAPPING_METHODS.contains(&method) {
                    *payload
                } else if OPTIONAL_PRESERVING_METHODS.contains(&method) {
                    ValueOrigin::Optional(payload)
                } else if OPTIONAL_FLAG_METHODS.contains(&method) {
                    ValueOrigin::PureStandardLibrary
                } else if matches!(method, "map" | "and_then") {
                    ValueOrigin::Optional(Box::new(ValueOrigin::Unknown))
                } else {
                    ValueOrigin::Unknown
                }
            }
            other => other,
        }
    }

    /// Reports whether a write! target is a String or a fmt::Formatter, so the write does no I/O.
    pub(crate) fn is_string_writer(&self, target: &Expr) -> bool {
        let Expr::Path(value) = peel(target) else {
            return false;
        };
        let Some(name) = value.path.get_ident().map(ToString::to_string) else {
            return false;
        };
        let Some(sources) = self.bindings.get(&name) else {
            return false;
        };
        sources.iter().all(|source| match source {
            BindingSource::Typed(declared) => matches!(
                crate::type_name(declared).as_deref(),
                Some("String" | "Formatter")
            ),
            BindingSource::Value(initializer) => is_string_value(initializer),
            _ => false,
        })
    }
}

/// Returns the origin of one element of an iterated or indexed value.
fn element_origin(container: ValueOrigin) -> ValueOrigin {
    match container {
        ValueOrigin::Collection(element) | ValueOrigin::Optional(element) => *element,
        origin @ (ValueOrigin::PureStandardLibrary
        | ValueOrigin::DexSdk
        | ValueOrigin::External(_)) => origin,
        ValueOrigin::FileType(_) | ValueOrigin::Unknown => ValueOrigin::Unknown,
    }
}

fn is_string_value(value: &Expr) -> bool {
    match peel(value) {
        Expr::Macro(formatted) => formatted.mac.path.is_ident("format"),
        Expr::Call(call) => {
            let Expr::Path(function) = peel(&call.func) else {
                return false;
            };
            let segments = path_segments(&function.path);
            matches!(
                segments
                    .iter()
                    .map(String::as_str)
                    .collect::<Vec<_>>()
                    .as_slice(),
                ["String", "new" | "with_capacity" | "from"]
            )
        }
        Expr::MethodCall(call) => {
            matches!(call.method.to_string().as_str(), "to_string" | "to_owned")
        }
        _ => false,
    }
}
