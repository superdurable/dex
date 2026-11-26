// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{BTreeMap, BTreeSet};

use proc_macro2::{Delimiter, Spacing, Span, TokenStream, TokenTree};
use serde_json::json;
use syn::punctuated::Punctuated;
use syn::spanned::Spanned;
use syn::visit::{self, Visit};
use syn::{Block, Expr, FnArg, Generics, Item, Pat, ReturnType, Signature, Token, Type};

use crate::expressions::{peel, source_text};
use crate::external_call_targets::{
    LOG_MACROS, PURE_MACROS, PURE_METHOD_NAMES, PathTarget, TypeScope, ValueOrigin, WRITE_MACROS,
};
use crate::{RustFlowGraph, RustFlowSource, WorkflowImplementation, impl_method, source_span};

/// Same-file helpers deeper than this count as external calls, as in the Go analyzer.
const MAXIMUM_HELPER_DEPTH: usize = 6;

/// Dex Flow and Step trait methods whose SDK defaults return the type name.
const DEX_TRAIT_DEFAULT_METHODS: &[&str] = &["flow_type", "step_type"];

/// Methods that copy, borrow, or unwrap a value and return the same kind of value.
const VALUE_PRESERVING_METHODS: &[&str] = &[
    "as_deref",
    "as_mut",
    "as_ref",
    "borrow",
    "clone",
    "cloned",
    "copied",
    "expect",
    "ok_or",
    "ok_or_else",
    "unwrap",
];

/// Option and Result combinators on a handle; their closure bodies are checked as part of the method.
const HANDLE_COMBINATOR_METHODS: &[&str] = &[
    "and_then", "filter", "inspect", "is_none", "is_some", "map", "map_err",
];

#[derive(Clone, Debug)]
struct PossibleExternalCall {
    callee: String,
    span: Span,
}

/// A same-file function or method whose body the finder can check.
#[derive(Clone, Copy)]
struct CheckedFunction<'source> {
    owner: &'source str,
    signature: &'source Signature,
    impl_generics: Option<&'source Generics>,
    block: &'source Block,
}

enum CallVerdict {
    Allowed,
    External,
    /// The call reaches a same-file helper that makes this external call.
    ThroughHelper(String),
}

#[derive(Clone, Copy)]
enum BindingSource<'ast> {
    Typed(&'ast Type),
    Value(&'ast Expr),
    /// A name bound inside a pattern that destructures this value.
    Destructured(&'ast Expr),
    Untyped,
}

/// Fills methodExternalCalls for Step methods with a fail-closed allowlist.
struct ExternalCallFinder<'source> {
    source: &'source RustFlowSource<'source>,
    /// Helpers whose result did not depend on the depth bound or a cycle.
    exact_results: BTreeMap<usize, Option<PossibleExternalCall>>,
    in_progress: BTreeSet<usize>,
}

impl<'source> RustFlowSource<'source> {
    pub(crate) fn record_method_external_calls(
        &self,
        step: &WorkflowImplementation<'source>,
        id: &str,
        graph: &mut RustFlowGraph,
    ) {
        let mut finder = ExternalCallFinder {
            source: self,
            exact_results: BTreeMap::new(),
            in_progress: BTreeSet::new(),
        };
        for phase in ["execute", "wait_for"] {
            let calls = match impl_method(step.declaration, phase) {
                Some(method) => {
                    let function = CheckedFunction {
                        owner: &step.rust_type,
                        signature: &method.sig,
                        impl_generics: Some(&step.declaration.generics),
                        block: &method.block,
                    };
                    finder.function_external_calls(function, 0, false).0
                }
                None if phase == "execute" => vec![PossibleExternalCall {
                    callee: "an execute method that dexcli cannot read".to_owned(),
                    span: step.declaration.span(),
                }],
                None => continue,
            };
            let calls: Vec<_> = calls
                .iter()
                .map(|call| json!({"callee": call.callee, "span": source_span(call.span)}))
                .collect();
            graph
                .facts("methodExternalCalls")
                .push(json!({"nodeId": id, "phase": phase, "calls": calls}));
        }
    }
}

impl<'source> ExternalCallFinder<'source> {
    /// Returns the possible external calls of function, and whether the result is exact.
    fn function_external_calls(
        &mut self,
        function: CheckedFunction<'source>,
        depth: usize,
        first_only: bool,
    ) -> (Vec<PossibleExternalCall>, bool) {
        let scope = TypeScope::new(
            function.owner,
            std::iter::once(&function.signature.generics).chain(function.impl_generics),
        );
        let mut bindings = BTreeMap::new();
        for input in &function.signature.inputs {
            if let FnArg::Typed(parameter) = input {
                let source = match parameter.pat.as_ref() {
                    Pat::Ident(_) => BindingSource::Typed(&parameter.ty),
                    _ => BindingSource::Untyped,
                };
                for name in pattern_names(&parameter.pat) {
                    bindings.entry(name).or_insert_with(Vec::new).push(source);
                }
            }
        }
        BindingCollector {
            bindings: &mut bindings,
        }
        .visit_block(function.block);
        let mut scan = MethodScan {
            finder: self,
            scope,
            bindings,
            depth,
            first_only,
            calls: Vec::new(),
            is_exact: true,
        };
        scan.visit_block(function.block);
        (scan.calls, scan.is_exact)
    }

    /// Returns the first possible external call of a same-file helper, and whether it is exact.
    fn helper_external_call(
        &mut self,
        function: CheckedFunction<'source>,
        depth: usize,
    ) -> (Option<PossibleExternalCall>, bool) {
        let key = std::ptr::from_ref(function.block).addr();
        if let Some(cached) = self.exact_results.get(&key) {
            return (cached.clone(), true);
        }
        if self.in_progress.contains(&key) {
            return (None, false);
        }
        if depth + 1 > MAXIMUM_HELPER_DEPTH {
            let deep = PossibleExternalCall {
                callee: "a helper chain deeper than dexcli follows".to_owned(),
                span: function.block.span(),
            };
            return (Some(deep), false);
        }
        self.in_progress.insert(key);
        let (calls, is_exact) = self.function_external_calls(function, depth + 1, true);
        self.in_progress.remove(&key);
        let result = calls.into_iter().next();
        if is_exact {
            self.exact_results.insert(key, result.clone());
        }
        (result, is_exact)
    }

    fn file_function(&self, name: &str) -> Option<CheckedFunction<'source>> {
        let Item::Fn(function) = self.source.declarations.get(name)? else {
            return None;
        };
        Some(CheckedFunction {
            owner: "",
            signature: &function.sig,
            impl_generics: None,
            block: &function.block,
        })
    }

    /// Returns every same-file method or associated function named name, of owner or of any type.
    fn file_methods(&self, owner: Option<&str>, name: &str) -> Vec<CheckedFunction<'source>> {
        let source = self.source;
        source
            .implementations
            .iter()
            .filter_map(|implementation| {
                let implementation: &'source syn::ItemImpl = implementation;
                let method = impl_method(implementation, name)?;
                let self_type = crate::type_name(&implementation.self_ty)?;
                if owner.is_some_and(|owner| owner != self_type) {
                    return None;
                }
                let owner = source
                    .declarations
                    .get_key_value(&self_type)
                    .map_or("", |(declared, _)| declared.as_str());
                Some(CheckedFunction {
                    owner,
                    signature: &method.sig,
                    impl_generics: Some(&implementation.generics),
                    block: &method.block,
                })
            })
            .collect()
    }

    fn return_origin(&self, function: CheckedFunction<'source>) -> ValueOrigin {
        let ReturnType::Type(_, declared) = &function.signature.output else {
            return ValueOrigin::PureStandardLibrary;
        };
        let scope = TypeScope::new(
            function.owner,
            std::iter::once(&function.signature.generics).chain(function.impl_generics),
        );
        self.source.type_origin(declared, &scope, 0)
    }
}

/// Collects every local name a function body binds, with what it is bound to.
struct BindingCollector<'collector, 'ast> {
    bindings: &'collector mut BTreeMap<String, Vec<BindingSource<'ast>>>,
}

impl BindingCollector<'_, '_> {
    fn bind<'ast>(
        bindings: &mut BTreeMap<String, Vec<BindingSource<'ast>>>,
        pattern: &Pat,
        source: BindingSource<'ast>,
    ) {
        for name in pattern_names(pattern) {
            bindings.entry(name).or_default().push(source);
        }
    }
}

impl<'ast> Visit<'ast> for BindingCollector<'_, 'ast> {
    fn visit_local(&mut self, local: &'ast syn::Local) {
        let initializer = local.init.as_ref().map(|init| init.expr.as_ref());
        let source = match (&local.pat, initializer) {
            (Pat::Type(typed), _) if matches!(typed.pat.as_ref(), Pat::Ident(_)) => {
                BindingSource::Typed(&typed.ty)
            }
            (Pat::Ident(_), Some(value)) => BindingSource::Value(value),
            (_, Some(value)) => BindingSource::Destructured(value),
            (_, None) => BindingSource::Untyped,
        };
        Self::bind(self.bindings, &local.pat, source);
        visit::visit_local(self, local);
    }

    fn visit_expr_match(&mut self, branch: &'ast syn::ExprMatch) {
        for arm in &branch.arms {
            Self::bind(
                self.bindings,
                &arm.pat,
                BindingSource::Destructured(&branch.expr),
            );
        }
        visit::visit_expr_match(self, branch);
    }

    fn visit_expr_let(&mut self, binding: &'ast syn::ExprLet) {
        Self::bind(
            self.bindings,
            &binding.pat,
            BindingSource::Destructured(&binding.expr),
        );
        visit::visit_expr_let(self, binding);
    }

    fn visit_expr_for_loop(&mut self, iteration: &'ast syn::ExprForLoop) {
        Self::bind(
            self.bindings,
            &iteration.pat,
            BindingSource::Destructured(&iteration.expr),
        );
        visit::visit_expr_for_loop(self, iteration);
    }

    fn visit_expr_closure(&mut self, closure: &'ast syn::ExprClosure) {
        for input in &closure.inputs {
            let source = match input {
                Pat::Type(typed) if matches!(typed.pat.as_ref(), Pat::Ident(_)) => {
                    BindingSource::Typed(&typed.ty)
                }
                _ => BindingSource::Untyped,
            };
            Self::bind(self.bindings, input, source);
        }
        visit::visit_expr_closure(self, closure);
    }

    fn visit_item(&mut self, _item: &'ast Item) {}
}

fn pattern_names(pattern: &Pat) -> Vec<String> {
    let mut names = Vec::new();
    collect_pattern_names(pattern, &mut names);
    names
}

fn collect_pattern_names(pattern: &Pat, names: &mut Vec<String>) {
    match pattern {
        Pat::Ident(identifier) => {
            names.push(identifier.ident.to_string());
            if let Some((_, inner)) = &identifier.subpat {
                collect_pattern_names(inner, names);
            }
        }
        Pat::Tuple(tuple) => tuple
            .elems
            .iter()
            .for_each(|element| collect_pattern_names(element, names)),
        Pat::TupleStruct(tuple) => tuple
            .elems
            .iter()
            .for_each(|element| collect_pattern_names(element, names)),
        Pat::Struct(value) => value
            .fields
            .iter()
            .for_each(|field| collect_pattern_names(&field.pat, names)),
        Pat::Slice(slice) => slice
            .elems
            .iter()
            .for_each(|element| collect_pattern_names(element, names)),
        Pat::Or(alternatives) => alternatives
            .cases
            .iter()
            .for_each(|case| collect_pattern_names(case, names)),
        Pat::Reference(reference) => collect_pattern_names(&reference.pat, names),
        Pat::Type(typed) => collect_pattern_names(&typed.pat, names),
        Pat::Paren(inner) => collect_pattern_names(&inner.pat, names),
        _ => {}
    }
}

/// Collects the possible external calls of one function body.
struct MethodScan<'scan, 'source, 'ast> {
    finder: &'scan mut ExternalCallFinder<'source>,
    scope: TypeScope,
    bindings: BTreeMap<String, Vec<BindingSource<'ast>>>,
    depth: usize,
    first_only: bool,
    calls: Vec<PossibleExternalCall>,
    is_exact: bool,
}

impl<'source, 'ast> MethodScan<'_, 'source, 'ast> {
    fn add(&mut self, span: Span, callee: String, verdict: CallVerdict) {
        let callee = match verdict {
            CallVerdict::Allowed => return,
            CallVerdict::External => callee,
            CallVerdict::ThroughHelper(inner) => format!("{callee} (calls {inner})"),
        };
        self.calls.push(PossibleExternalCall { callee, span });
    }

    fn helper_verdict(&mut self, helpers: Vec<CheckedFunction<'source>>) -> CallVerdict {
        for helper in helpers {
            let (inner, is_exact) = self.finder.helper_external_call(helper, self.depth);
            self.is_exact = self.is_exact && is_exact;
            if let Some(inner) = inner {
                return CallVerdict::ThroughHelper(inner.callee);
            }
        }
        CallVerdict::Allowed
    }

    /// Checks a method of a same-file type; an unmatched name must be a pure standard method.
    fn file_type_method_verdict(&mut self, owner: Option<&str>, method: &str) -> CallVerdict {
        let helpers = self.finder.file_methods(owner, method);
        if !helpers.is_empty() {
            return self.helper_verdict(helpers);
        }
        let is_dex_trait_default = owner.is_some() && DEX_TRAIT_DEFAULT_METHODS.contains(&method);
        if PURE_METHOD_NAMES.contains(&method) || is_dex_trait_default {
            CallVerdict::Allowed
        } else {
            CallVerdict::External
        }
    }

    fn is_binding(&self, name: &str) -> bool {
        self.bindings.contains_key(name)
    }

    fn path_call_verdict(&mut self, path: &syn::Path) -> CallVerdict {
        let segments = path_segments(path);
        if let [name] = segments.as_slice()
            && self.is_binding(name)
        {
            return CallVerdict::External;
        }
        let last = segments.last().cloned().unwrap_or_default();
        match self.finder.source.path_target(&segments, &self.scope) {
            PathTarget::DexSdk | PathTarget::PureStandardLibrary => CallVerdict::Allowed,
            PathTarget::DexSdkExternal
            | PathTarget::ExternalStandardLibrary
            | PathTarget::External => CallVerdict::External,
            PathTarget::FileItem(name) if name == last => {
                if let Some(function) = self.finder.file_function(&name) {
                    return self.helper_verdict(vec![function]);
                }
                match self.finder.source.declarations.get(&name) {
                    Some(Item::Struct(_) | Item::Enum(_)) => CallVerdict::Allowed,
                    _ => CallVerdict::External,
                }
            }
            PathTarget::FileItem(owner) => self.associated_call_verdict(&owner, &last),
            PathTarget::SelfType => {
                let owner = self.scope.owner.clone();
                self.associated_call_verdict(&owner, &last)
            }
        }
    }

    fn associated_call_verdict(&mut self, owner: &str, function: &str) -> CallVerdict {
        if function.starts_with(char::is_uppercase) {
            // An enum variant constructor runs no code.
            return CallVerdict::Allowed;
        }
        self.file_type_method_verdict(Some(owner), function)
    }

    fn method_call_verdict(&mut self, call: &syn::ExprMethodCall) -> CallVerdict {
        let method = call.method.to_string();
        match self.expression_origin(&call.receiver, 0) {
            ValueOrigin::DexSdk | ValueOrigin::PureStandardLibrary | ValueOrigin::Collection => {
                CallVerdict::Allowed
            }
            // Cloning or unwrapping a dependency handle calls nothing; the next call on it is reported.
            ValueOrigin::External
                if VALUE_PRESERVING_METHODS.contains(&method.as_str())
                    || HANDLE_COMBINATOR_METHODS.contains(&method.as_str()) =>
            {
                CallVerdict::Allowed
            }
            // A chain on an already reported call, such as client.send()?.unwrap(), is reported once.
            ValueOrigin::External if self.is_reported_call(&call.receiver) => CallVerdict::Allowed,
            ValueOrigin::External => CallVerdict::External,
            ValueOrigin::FileType(owner) => self.file_type_method_verdict(Some(&owner), &method),
            ValueOrigin::Unknown => self.file_type_method_verdict(None, &method),
        }
    }

    fn is_reported_call(&mut self, expression: &Expr) -> bool {
        let verdict = match peel(expression) {
            Expr::MethodCall(call) => self.method_call_verdict(call),
            Expr::Call(call) => match peel(&call.func) {
                Expr::Path(function) if function.qself.is_none() => {
                    self.path_call_verdict(&function.path)
                }
                Expr::Closure(_) => CallVerdict::Allowed,
                _ => CallVerdict::External,
            },
            _ => CallVerdict::Allowed,
        };
        !matches!(verdict, CallVerdict::Allowed)
    }

    fn binding_origin(&self, name: &str, depth: usize) -> ValueOrigin {
        let Some(sources) = self.bindings.get(name) else {
            return ValueOrigin::Unknown;
        };
        let mut origins = sources.iter().map(|source| match source {
            BindingSource::Typed(declared) => {
                self.finder.source.type_origin(declared, &self.scope, 0)
            }
            BindingSource::Value(value) => self.expression_origin(value, depth + 1),
            BindingSource::Destructured(value) => match self.expression_origin(value, depth + 1) {
                ValueOrigin::External => ValueOrigin::External,
                _ => ValueOrigin::Unknown,
            },
            BindingSource::Untyped => ValueOrigin::Unknown,
        });
        let first = origins.next().unwrap_or(ValueOrigin::Unknown);
        origins.fold(first, ValueOrigin::merge)
    }

    fn expression_origin(&self, expression: &Expr, depth: usize) -> ValueOrigin {
        if depth >= 16 {
            return ValueOrigin::Unknown;
        }
        match peel(expression) {
            Expr::Lit(_)
            | Expr::Array(_)
            | Expr::Tuple(_)
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
            Expr::Index(index) => match self.expression_origin(&index.expr, depth + 1) {
                ValueOrigin::External => ValueOrigin::External,
                _ => ValueOrigin::Unknown,
            },
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
        crate::expressions::block_tail(block).map_or(ValueOrigin::Unknown, |tail| {
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
            PathTarget::DexSdkExternal
            | PathTarget::ExternalStandardLibrary
            | PathTarget::External => ValueOrigin::External,
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
            ValueOrigin::Collection => return ValueOrigin::Unknown,
            other => return other,
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
            return ValueOrigin::External;
        };
        let segments = path_segments(&function.path);
        if let [name] = segments.as_slice()
            && self.is_binding(name)
        {
            return ValueOrigin::External;
        }
        let last = segments.last().cloned().unwrap_or_default();
        let source = self.finder.source;
        match source.path_target(&segments, &self.scope) {
            PathTarget::DexSdk => ValueOrigin::DexSdk,
            PathTarget::PureStandardLibrary => ValueOrigin::container(
                call.args
                    .iter()
                    .map(|argument| self.expression_origin(argument, depth + 1)),
            ),
            PathTarget::FileItem(name) if name == last => match self.finder.file_function(&name) {
                Some(function) => self.finder.return_origin(function),
                None => ValueOrigin::FileType(name),
            },
            PathTarget::FileItem(owner) => self.associated_result_origin(&owner, &last),
            PathTarget::SelfType => self.associated_result_origin(&self.scope.owner, &last),
            PathTarget::DexSdkExternal
            | PathTarget::ExternalStandardLibrary
            | PathTarget::External => ValueOrigin::External,
        }
    }

    fn associated_result_origin(&self, owner: &str, function: &str) -> ValueOrigin {
        if function.starts_with(char::is_uppercase) {
            return ValueOrigin::FileType(owner.to_owned());
        }
        self.finder
            .file_methods(Some(owner), function)
            .into_iter()
            .map(|method| self.finder.return_origin(method))
            .reduce(ValueOrigin::merge)
            .unwrap_or(ValueOrigin::Unknown)
    }

    fn method_result_origin(&self, call: &syn::ExprMethodCall, depth: usize) -> ValueOrigin {
        let receiver = self.expression_origin(&call.receiver, depth + 1);
        if call.method == "clone" {
            return receiver;
        }
        let method = call.method.to_string();
        match receiver {
            ValueOrigin::FileType(owner) => {
                let result = self.associated_result_origin(&owner, &method);
                if result == ValueOrigin::Unknown
                    && VALUE_PRESERVING_METHODS.contains(&method.as_str())
                {
                    // Unwrapping an Option or Result of a same-file type keeps that type.
                    return ValueOrigin::FileType(owner);
                }
                result
            }
            // An element taken from a collection has an unknown type.
            ValueOrigin::Collection => ValueOrigin::Unknown,
            other => other,
        }
    }

    /// Reports whether a write! target is a String or a fmt::Formatter, so the write does no I/O.
    fn is_string_writer(&self, target: &Expr) -> bool {
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
            BindingSource::Destructured(_) | BindingSource::Untyped => false,
        })
    }

    fn scan_macro(&mut self, value: &syn::Macro) {
        let name = value
            .path
            .segments
            .last()
            .map(|segment| segment.ident.to_string())
            .unwrap_or_default();
        let callee = format!("{name}!");
        let name = name.as_str();
        if !PURE_MACROS.contains(&name)
            && !LOG_MACROS.contains(&name)
            && !WRITE_MACROS.contains(&name)
        {
            self.add(value.span(), callee, CallVerdict::External);
            return;
        }
        let Some(arguments) = macro_argument_expressions(value.tokens.clone()) else {
            let unreadable = format!("{name}! with arguments dexcli cannot read");
            self.add(value.span(), unreadable, CallVerdict::External);
            return;
        };
        if WRITE_MACROS.contains(&name)
            && !arguments
                .first()
                .is_some_and(|target| self.is_string_writer(target))
        {
            self.add(value.span(), callee, CallVerdict::External);
        }
        self.scan_macro_arguments(&arguments);
    }

    /// Scans parsed macro arguments, which live shorter than the function body.
    fn scan_macro_arguments(&mut self, arguments: &[Expr]) {
        let mut bindings = self.bindings.clone();
        for argument in arguments {
            BindingCollector {
                bindings: &mut bindings,
            }
            .visit_expr(argument);
        }
        let mut nested = MethodScan {
            finder: &mut *self.finder,
            scope: self.scope.clone(),
            bindings,
            depth: self.depth,
            first_only: self.first_only,
            calls: Vec::new(),
            is_exact: true,
        };
        for argument in arguments {
            nested.visit_expr(argument);
        }
        let (calls, is_exact) = (nested.calls, nested.is_exact);
        self.calls.extend(calls);
        self.is_exact = self.is_exact && is_exact;
    }

    fn is_done(&self) -> bool {
        self.first_only && !self.calls.is_empty()
    }
}

impl<'ast> Visit<'ast> for MethodScan<'_, '_, 'ast> {
    fn visit_expr(&mut self, expression: &'ast Expr) {
        if !self.is_done() {
            visit::visit_expr(self, expression);
        }
    }

    fn visit_expr_call(&mut self, call: &'ast syn::ExprCall) {
        match peel(&call.func) {
            Expr::Path(function) if function.qself.is_none() => {
                let verdict = self.path_call_verdict(&function.path);
                self.add(call.span(), source_text(&call.func), verdict);
            }
            Expr::Closure(_) => self.visit_expr(&call.func),
            _ => {
                self.add(call.span(), source_text(&call.func), CallVerdict::External);
                self.visit_expr(&call.func);
            }
        }
        for argument in &call.args {
            self.visit_expr(argument);
        }
    }

    fn visit_expr_method_call(&mut self, call: &'ast syn::ExprMethodCall) {
        let verdict = self.method_call_verdict(call);
        let callee = format!("{}.{}", source_text(&call.receiver), call.method);
        self.add(call.span(), callee, verdict);
        self.visit_expr(&call.receiver);
        for argument in &call.args {
            self.visit_expr(argument);
        }
    }

    /// A function used as a value, such as a map_err argument, may run later.
    fn visit_expr_path(&mut self, value: &'ast syn::ExprPath) {
        if value.qself.is_some() {
            return;
        }
        let segments = path_segments(&value.path);
        let is_function = match segments.as_slice() {
            [name] => !self.is_binding(name) && self.finder.file_function(name).is_some(),
            [.., last] => last.starts_with(char::is_lowercase),
            [] => false,
        };
        if is_function {
            let verdict = self.path_call_verdict(&value.path);
            self.add(value.span(), source_text(value), verdict);
        }
    }

    fn visit_macro(&mut self, value: &'ast syn::Macro) {
        if !self.is_done() {
            self.scan_macro(value);
        }
    }

    fn visit_item(&mut self, _item: &'ast Item) {}
}

fn path_segments(path: &syn::Path) -> Vec<String> {
    path.segments
        .iter()
        .map(|segment| segment.ident.to_string())
        .collect()
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

/// Parses macro arguments as expressions, reading json! objects and tracing fields; None means unreadable.
fn macro_argument_expressions(tokens: TokenStream) -> Option<Vec<Expr>> {
    if let Ok(arguments) = syn::parse::Parser::parse2(
        Punctuated::<Expr, Token![,]>::parse_terminated,
        tokens.clone(),
    ) {
        return Some(arguments.into_iter().collect());
    }
    let mut expressions = Vec::new();
    for chunk in split_top_level(tokens) {
        if let [TokenTree::Group(group)] = chunk.as_slice()
            && matches!(group.delimiter(), Delimiter::Brace | Delimiter::Bracket)
        {
            expressions.extend(macro_argument_expressions(group.stream())?);
            continue;
        }
        let value: TokenStream = field_value(&chunk).iter().cloned().collect();
        if value.is_empty() {
            continue;
        }
        expressions.push(syn::parse2::<Expr>(value).ok()?);
    }
    Some(expressions)
}

fn split_top_level(tokens: TokenStream) -> Vec<Vec<TokenTree>> {
    let mut chunks = vec![Vec::new()];
    for token in tokens {
        match &token {
            TokenTree::Punct(punct) if matches!(punct.as_char(), ',' | ';') => {
                chunks.push(Vec::new());
            }
            _ => chunks
                .last_mut()
                .expect("chunks always holds one element")
                .push(token),
        }
    }
    chunks.retain(|chunk| !chunk.is_empty());
    chunks
}

/// Returns the value of a `name = value`, `key: value`, or `%value` macro field.
fn field_value(chunk: &[TokenTree]) -> &[TokenTree] {
    let mut start = 0;
    for (index, token) in chunk.iter().enumerate() {
        let TokenTree::Punct(punct) = token else {
            continue;
        };
        let follows_joint = index > 0
            && matches!(&chunk[index - 1], TokenTree::Punct(previous) if previous.spacing() == Spacing::Joint);
        if matches!(punct.as_char(), '=' | ':')
            && punct.spacing() == Spacing::Alone
            && !follows_joint
        {
            start = index + 1;
            break;
        }
    }
    let mut value = &chunk[start..];
    while let [TokenTree::Punct(sigil), rest @ ..] = value
        && matches!(sigil.as_char(), '%' | '?')
    {
        value = rest;
    }
    value
}
