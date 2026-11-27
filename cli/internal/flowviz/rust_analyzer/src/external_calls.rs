// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::cmp::Reverse;
use std::collections::{BTreeMap, BTreeSet};

use proc_macro2::Span;
use serde_json::json;
use syn::spanned::Spanned;
use syn::visit::{self, Visit};
use syn::{Block, Expr, FnArg, Generics, Item, Pat, ReturnType, Signature, Type};

use crate::expressions::{peel, source_text};
use crate::external_call_macros::macro_argument_expressions;
use crate::external_call_targets::{
    Certainty, DATA_PURE_METHOD_NAMES, DataType, LOG_MACROS, PRINT_MACROS, PURE_MACROS, PathTarget,
    TypeScope, ValueOrigin, WRITE_MACROS,
};
use crate::{RustFlowGraph, RustFlowSource, impl_method, source_span};

/// Same-file helpers deeper than this count as external calls, as in the Go analyzer.
const MAXIMUM_HELPER_DEPTH: usize = 6;

/// Dex Flow and Step trait methods whose SDK defaults return the type name.
pub(crate) const DEX_TRAIT_DEFAULT_METHODS: &[&str] = &["flow_type", "step_type"];

/// Methods that only copy or borrow a dependency handle; the next call on it is reported.
const HANDLE_COPY_METHODS: &[&str] = &["as_deref", "as_ref", "borrow", "clone"];

const STEP_METHOD_PHASES: [&str; 2] = ["execute", "wait_for"];

#[derive(Clone, Debug)]
struct PossibleExternalCall {
    callee: String,
    span: Span,
    certainty: Certainty,
}

/// A same-file function or method whose body the finder can check.
#[derive(Clone, Copy)]
pub(crate) struct CheckedFunction<'source> {
    owner: &'source str,
    signature: &'source Signature,
    impl_generics: Option<&'source Generics>,
    block: &'source Block,
}

enum CallVerdict {
    Allowed,
    External(Certainty),
    /// The call reaches a same-file helper that makes this external call.
    ThroughHelper(String, Certainty),
}

#[derive(Clone, Copy)]
pub(crate) enum BindingSource<'ast> {
    Typed(&'ast Type),
    /// The Step input parameter, whose deserialized value holds no service.
    StepInput(&'ast Type),
    Value(&'ast Expr),
    /// The payload of a `Some(name)` or `Ok(name)` pattern matched against this value.
    Unwrapped(&'ast Expr),
    /// An element of this iterated value.
    Element(&'ast Expr),
    /// A parameter of a closure passed to a combinator method of receiver, such as map or and_then.
    CombinatorParameter {
        receiver: &'ast Expr,
        method: &'ast syn::Ident,
        index: usize,
        /// The closure body, the only place where this binding is visible.
        body: &'ast Expr,
    },
    /// A name bound inside another pattern that destructures this value.
    Destructured(&'ast Expr),
    Untyped,
}

/// Fills methodExternalCalls for Step methods with a fail-closed allowlist.
pub(crate) struct ExternalCallFinder<'source> {
    pub(crate) source: &'source RustFlowSource<'source>,
    /// Helpers whose result did not depend on the depth bound or a cycle.
    exact_results: BTreeMap<usize, Option<PossibleExternalCall>>,
    in_progress: BTreeSet<usize>,
}

impl<'source> RustFlowSource<'source> {
    /// Records markers only for Step methods that hold a lock an RPC also takes.
    pub(crate) fn record_method_external_calls(
        &self,
        registered: &BTreeSet<String>,
        graph: &mut RustFlowGraph,
    ) {
        let shared_methods = shared_lock_methods(graph);
        let mut finder = ExternalCallFinder {
            source: self,
            exact_results: BTreeMap::new(),
            in_progress: BTreeSet::new(),
        };
        for rust_type in registered {
            let Some(step) = self.steps.get(rust_type) else {
                continue;
            };
            let id = format!("step:{}", step.name);
            for phase in STEP_METHOD_PHASES {
                if !shared_methods.contains(&(id.clone(), phase.to_owned())) {
                    continue;
                }
                let mut calls = match impl_method(step.declaration, phase) {
                    Some(method) => {
                        let function = CheckedFunction {
                            owner: &step.rust_type,
                            signature: &method.sig,
                            impl_generics: Some(&step.declaration.generics),
                            block: &method.block,
                        };
                        finder.function_external_calls(function, 0, false).0
                    }
                    // The SDK default wait_for skips the wait and calls nothing.
                    None if phase == "wait_for" => Vec::new(),
                    None => vec![PossibleExternalCall {
                        callee: "an execute method that dexcli cannot read".to_owned(),
                        span: step.declaration.span(),
                        certainty: Certainty::Possible,
                    }],
                };
                calls.sort_by_key(|call| Reverse(call.certainty));
                let calls: Vec<_> = calls
                    .iter()
                    .map(|call| {
                        let mut entry =
                            json!({"callee": call.callee, "span": source_span(call.span)});
                        if call.certainty == Certainty::Possible {
                            entry["certainty"] = json!("uncertain");
                        }
                        entry
                    })
                    .collect();
                graph
                    .facts("methodExternalCalls")
                    .push(json!({"nodeId": id, "phase": phase, "calls": calls}));
            }
        }
    }
}

/// Returns each (Step node ID, phase) whose locks intersect the locks of any RPC.
fn shared_lock_methods(graph: &mut RustFlowGraph) -> BTreeSet<(String, String)> {
    let mut rpc_locks = BTreeSet::new();
    let mut method_locks = Vec::new();
    for edge in graph.graph_items("edges").iter() {
        if edge["kind"] != "resource_lock" {
            continue;
        }
        let (Some(from), Some(to), Some(phase)) = (
            edge["from"].as_str(),
            edge["to"].as_str(),
            edge["metadata"]["phase"].as_str(),
        ) else {
            continue;
        };
        if phase == "rpc" {
            rpc_locks.insert(to.to_owned());
        } else if STEP_METHOD_PHASES.contains(&phase) {
            method_locks.push((from.to_owned(), phase.to_owned(), to.to_owned()));
        }
    }
    method_locks
        .into_iter()
        .filter(|(_, _, resource)| rpc_locks.contains(resource))
        .map(|(from, phase, _)| (from, phase))
        .collect()
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
        let typed_parameters = function
            .signature
            .inputs
            .iter()
            .filter_map(|input| match input {
                FnArg::Typed(parameter) => Some(parameter),
                FnArg::Receiver(_) => None,
            });
        for (index, parameter) in typed_parameters.enumerate() {
            // Depth 0 is the Step method, whose second parameter is the deserialized Step input.
            let is_step_input = depth == 0 && index == 1;
            let source = match parameter.pat.as_ref() {
                Pat::Ident(_) if is_step_input => BindingSource::StepInput(&parameter.ty),
                Pat::Ident(_) => BindingSource::Typed(&parameter.ty),
                _ => BindingSource::Untyped,
            };
            bind_names(&mut bindings, &parameter.pat, source);
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

    /// Returns the most certain first external call of a same-file helper, and whether it is exact.
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
                certainty: Certainty::Possible,
            };
            return (Some(deep), false);
        }
        self.in_progress.insert(key);
        let (mut calls, is_exact) = self.function_external_calls(function, depth + 1, true);
        self.in_progress.remove(&key);
        calls.sort_by_key(|call| Reverse(call.certainty));
        let result = calls.into_iter().next();
        if is_exact {
            self.exact_results.insert(key, result.clone());
        }
        (result, is_exact)
    }

    pub(crate) fn file_function(&self, name: &str) -> Option<CheckedFunction<'source>> {
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

    /// Returns every same-file method or associated function of owner named name.
    pub(crate) fn file_methods(&self, owner: &str, name: &str) -> Vec<CheckedFunction<'source>> {
        let source = self.source;
        source
            .implementations
            .iter()
            .filter_map(|implementation| {
                let implementation: &'source syn::ItemImpl = implementation;
                let method = impl_method(implementation, name)?;
                let self_type = crate::type_name(&implementation.self_ty)?;
                if owner != self_type {
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

    pub(crate) fn return_origin(&self, function: CheckedFunction<'source>) -> ValueOrigin {
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

fn bind_names<'ast>(
    bindings: &mut BTreeMap<String, Vec<BindingSource<'ast>>>,
    pattern: &Pat,
    source: BindingSource<'ast>,
) {
    for name in pattern_names(pattern) {
        bindings.entry(name).or_default().push(source);
    }
}

/// Binds the names of pattern matched against value, keeping tuple elements and Some or Ok payloads.
fn bind_matched<'ast>(
    bindings: &mut BTreeMap<String, Vec<BindingSource<'ast>>>,
    pattern: &'ast Pat,
    value: &'ast Expr,
) {
    match pattern {
        Pat::Ident(_) => bind_names(bindings, pattern, BindingSource::Value(value)),
        Pat::Reference(reference) => bind_matched(bindings, &reference.pat, value),
        Pat::Paren(inner) => bind_matched(bindings, &inner.pat, value),
        Pat::Type(typed) if matches!(typed.pat.as_ref(), Pat::Ident(_)) => {
            bind_names(bindings, pattern, BindingSource::Typed(&typed.ty));
        }
        Pat::Tuple(tuple) => match peel(value) {
            Expr::Tuple(values) if values.elems.len() == tuple.elems.len() => {
                for (element, value) in tuple.elems.iter().zip(&values.elems) {
                    bind_matched(bindings, element, value);
                }
            }
            _ => bind_names(bindings, pattern, BindingSource::Destructured(value)),
        },
        Pat::TupleStruct(variant)
            if variant.elems.len() == 1
                && variant
                    .path
                    .segments
                    .last()
                    .is_some_and(|segment| segment.ident == "Some" || segment.ident == "Ok") =>
        {
            let payload = variant.elems.first().expect("the pattern has one element");
            let source = if matches!(payload, Pat::Ident(_)) {
                BindingSource::Unwrapped(value)
            } else {
                BindingSource::Destructured(value)
            };
            bind_names(bindings, payload, source);
        }
        _ => bind_names(bindings, pattern, BindingSource::Destructured(value)),
    }
}

impl<'ast> Visit<'ast> for BindingCollector<'_, 'ast> {
    fn visit_local(&mut self, local: &'ast syn::Local) {
        match local.init.as_ref() {
            Some(init) => bind_matched(self.bindings, &local.pat, &init.expr),
            None => match &local.pat {
                Pat::Type(typed) if matches!(typed.pat.as_ref(), Pat::Ident(_)) => {
                    bind_names(self.bindings, &local.pat, BindingSource::Typed(&typed.ty));
                }
                pattern => bind_names(self.bindings, pattern, BindingSource::Untyped),
            },
        }
        visit::visit_local(self, local);
    }

    fn visit_expr_match(&mut self, branch: &'ast syn::ExprMatch) {
        for arm in &branch.arms {
            bind_matched(self.bindings, &arm.pat, &branch.expr);
        }
        visit::visit_expr_match(self, branch);
    }

    fn visit_expr_let(&mut self, binding: &'ast syn::ExprLet) {
        bind_matched(self.bindings, &binding.pat, &binding.expr);
        visit::visit_expr_let(self, binding);
    }

    fn visit_expr_for_loop(&mut self, iteration: &'ast syn::ExprForLoop) {
        let source = match iteration.pat.as_ref() {
            Pat::Ident(_) => BindingSource::Element(&iteration.expr),
            _ => BindingSource::Destructured(&iteration.expr),
        };
        bind_names(self.bindings, &iteration.pat, source);
        visit::visit_expr_for_loop(self, iteration);
    }

    /// Binds the parameters of a closure passed to a combinator, such as map, to the receiver's elements.
    fn visit_expr_method_call(&mut self, call: &'ast syn::ExprMethodCall) {
        self.visit_expr(&call.receiver);
        for argument in &call.args {
            let Expr::Closure(closure) = argument else {
                self.visit_expr(argument);
                continue;
            };
            for (index, input) in closure.inputs.iter().enumerate() {
                let source = match input {
                    Pat::Type(typed) if matches!(typed.pat.as_ref(), Pat::Ident(_)) => {
                        BindingSource::Typed(&typed.ty)
                    }
                    Pat::Ident(_) => BindingSource::CombinatorParameter {
                        receiver: &call.receiver,
                        method: &call.method,
                        index,
                        body: &closure.body,
                    },
                    _ => BindingSource::Untyped,
                };
                bind_names(self.bindings, input, source);
            }
            self.visit_expr(&closure.body);
        }
    }

    fn visit_expr_closure(&mut self, closure: &'ast syn::ExprClosure) {
        for input in &closure.inputs {
            let source = match input {
                Pat::Type(typed) if matches!(typed.pat.as_ref(), Pat::Ident(_)) => {
                    BindingSource::Typed(&typed.ty)
                }
                _ => BindingSource::Untyped,
            };
            bind_names(self.bindings, input, source);
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
pub(crate) struct MethodScan<'scan, 'source, 'ast> {
    pub(crate) finder: &'scan mut ExternalCallFinder<'source>,
    pub(crate) scope: TypeScope,
    pub(crate) bindings: BTreeMap<String, Vec<BindingSource<'ast>>>,
    depth: usize,
    first_only: bool,
    calls: Vec<PossibleExternalCall>,
    is_exact: bool,
}

impl<'source, 'ast> MethodScan<'_, 'source, 'ast> {
    fn add(&mut self, span: Span, callee: String, verdict: CallVerdict) {
        let (callee, certainty) = match verdict {
            CallVerdict::Allowed => return,
            CallVerdict::External(certainty) => (callee, certainty),
            CallVerdict::ThroughHelper(inner, certainty) => {
                (format!("{callee} (calls {inner})"), certainty)
            }
        };
        self.calls.push(PossibleExternalCall {
            callee,
            span,
            certainty,
        });
    }

    fn helper_verdict(&mut self, helpers: Vec<CheckedFunction<'source>>) -> CallVerdict {
        let mut verdict = CallVerdict::Allowed;
        for helper in helpers {
            let (inner, is_exact) = self.finder.helper_external_call(helper, self.depth);
            self.is_exact = self.is_exact && is_exact;
            let Some(inner) = inner else {
                continue;
            };
            let is_more_certain = match &verdict {
                CallVerdict::ThroughHelper(_, certainty) => inner.certainty > *certainty,
                _ => true,
            };
            if is_more_certain {
                verdict = CallVerdict::ThroughHelper(inner.callee, inner.certainty);
            }
        }
        verdict
    }

    /// Checks a method or associated function of a same-file type.
    fn file_type_method_verdict(&mut self, owner: &str, method: &str) -> CallVerdict {
        let helpers = self.finder.file_methods(owner, method);
        if !helpers.is_empty() {
            return self.helper_verdict(helpers);
        }
        if DEX_TRAIT_DEFAULT_METHODS.contains(&method) {
            return CallVerdict::Allowed;
        }
        if self.finder.source.derives_method(owner, method) {
            return self.derived_method_verdict(owner, method, &mut BTreeSet::new());
        }
        CallVerdict::External(Certainty::Possible)
    }

    /// Checks a derived Clone or Default, which calls the same method of every field type.
    fn derived_method_verdict(
        &mut self,
        owner: &str,
        method: &str,
        visited: &mut BTreeSet<String>,
    ) -> CallVerdict {
        if !visited.insert(owner.to_owned()) {
            // A recursive type is checked by the outer call.
            return CallVerdict::Allowed;
        }
        if visited.len() > MAXIMUM_HELPER_DEPTH {
            let deep = "a derived field chain deeper than dexcli follows".to_owned();
            return CallVerdict::ThroughHelper(deep, Certainty::Possible);
        }
        let source = self.finder.source;
        let Some((fields, generics)) = source.declared_field_types(owner) else {
            return CallVerdict::External(Certainty::Possible);
        };
        let scope = TypeScope::new(owner, [generics]);
        for field in fields {
            let verdict = self.field_trait_verdict(field, method, &scope, visited);
            if !matches!(verdict, CallVerdict::Allowed) {
                return verdict;
            }
        }
        CallVerdict::Allowed
    }

    /// Checks the Clone or Default method that a derived impl calls for one field type.
    fn field_trait_verdict(
        &mut self,
        field: &Type,
        method: &str,
        scope: &TypeScope,
        visited: &mut BTreeSet<String>,
    ) -> CallVerdict {
        let unresolved = || {
            let callee = format!("{}::{method}", source_text(field));
            CallVerdict::ThroughHelper(callee, Certainty::Possible)
        };
        let elements: Vec<&Type> = match field {
            Type::Reference(_) => return CallVerdict::Allowed,
            Type::Paren(inner) => vec![&inner.elem],
            Type::Group(inner) => vec![&inner.elem],
            Type::Array(array) => vec![&array.elem],
            Type::Slice(slice) => vec![&slice.elem],
            Type::Tuple(tuple) => tuple.elems.iter().collect(),
            Type::Path(path) if path.qself.is_none() => {
                let segments = path_segments(&path.path);
                let last = segments.last().cloned().unwrap_or_default();
                let source = self.finder.source;
                match source.path_target(&segments, scope) {
                    // Cloning an Arc or Rc only counts a reference.
                    PathTarget::PureStandardLibrary
                        if method == "clone" && matches!(last.as_str(), "Arc" | "Rc") =>
                    {
                        return CallVerdict::Allowed;
                    }
                    // Option, collections, and similar types default to empty without calling T::default.
                    PathTarget::PureStandardLibrary
                        if method == "default"
                            && !matches!(
                                last.as_str(),
                                "Arc"
                                    | "Box"
                                    | "Cell"
                                    | "Cow"
                                    | "Mutex"
                                    | "Rc"
                                    | "RefCell"
                                    | "RwLock"
                            ) =>
                    {
                        return CallVerdict::Allowed;
                    }
                    PathTarget::PureStandardLibrary | PathTarget::DexSdk => {
                        type_arguments(&path.path)
                    }
                    PathTarget::FileItem(name) => match source.declarations.get(&name) {
                        Some(Item::Type(alias)) => vec![alias.ty.as_ref()],
                        Some(Item::Struct(_) | Item::Enum(_)) => {
                            return self.type_trait_verdict(&name, method, visited);
                        }
                        _ => return unresolved(),
                    },
                    PathTarget::SelfType => {
                        let owner = scope.owner.clone();
                        return self.type_trait_verdict(&owner, method, visited);
                    }
                    PathTarget::DexSdkExternal
                    | PathTarget::ExternalStandardLibrary
                    | PathTarget::External => return unresolved(),
                }
            }
            _ => return unresolved(),
        };
        for element in elements {
            let verdict = self.field_trait_verdict(element, method, scope, visited);
            if !matches!(verdict, CallVerdict::Allowed) {
                return verdict;
            }
        }
        CallVerdict::Allowed
    }

    /// Checks a same-file type's Clone or Default: its explicit impl, or its derived fields.
    fn type_trait_verdict(
        &mut self,
        owner: &str,
        method: &str,
        visited: &mut BTreeSet<String>,
    ) -> CallVerdict {
        let helpers = self.finder.file_methods(owner, method);
        if !helpers.is_empty() {
            return match self.helper_verdict(helpers) {
                CallVerdict::ThroughHelper(inner, certainty) => CallVerdict::ThroughHelper(
                    format!("{owner}::{method} (calls {inner})"),
                    certainty,
                ),
                verdict => verdict,
            };
        }
        if self.finder.source.derives_method(owner, method) {
            return self.derived_method_verdict(owner, method, visited);
        }
        CallVerdict::ThroughHelper(format!("{owner}::{method}"), Certainty::Possible)
    }

    /// Checks a method on deserialized data, according to how well its type is known.
    fn data_method_verdict(&mut self, data_type: &DataType, method: &str) -> CallVerdict {
        match data_type {
            DataType::FileType(owner) => self.file_type_method_verdict(owner, method),
            // Another file's data type may define an I/O method with any name.
            DataType::Custom => CallVerdict::External(Certainty::Possible),
            DataType::Unresolved if DATA_PURE_METHOD_NAMES.contains(&method) => {
                CallVerdict::Allowed
            }
            DataType::Unresolved => CallVerdict::External(Certainty::Possible),
        }
    }

    pub(crate) fn is_binding(&self, name: &str) -> bool {
        self.bindings.contains_key(name)
    }

    fn path_call_verdict(&mut self, path: &syn::Path) -> CallVerdict {
        let segments = path_segments(path);
        if let [name] = segments.as_slice()
            && self.is_binding(name)
        {
            // A call through a function value is dynamic, so it counts as definite.
            return CallVerdict::External(Certainty::Definite);
        }
        let last = segments.last().cloned().unwrap_or_default();
        match self.finder.source.path_target(&segments, &self.scope) {
            PathTarget::DexSdk | PathTarget::PureStandardLibrary => CallVerdict::Allowed,
            PathTarget::DexSdkExternal | PathTarget::ExternalStandardLibrary => {
                CallVerdict::External(Certainty::Definite)
            }
            PathTarget::External => CallVerdict::External(Certainty::Possible),
            PathTarget::FileItem(name) if name == last => {
                if let Some(function) = self.finder.file_function(&name) {
                    return self.helper_verdict(vec![function]);
                }
                match self.finder.source.declarations.get(&name) {
                    Some(Item::Struct(_) | Item::Enum(_)) => CallVerdict::Allowed,
                    _ => CallVerdict::External(Certainty::Possible),
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
        self.file_type_method_verdict(owner, function)
    }

    fn method_call_verdict(&mut self, call: &syn::ExprMethodCall) -> CallVerdict {
        let method = call.method.to_string();
        match self.expression_origin(&call.receiver, 0) {
            ValueOrigin::DexSdk
            | ValueOrigin::PureStandardLibrary
            | ValueOrigin::Collection(_)
            | ValueOrigin::Optional(_) => CallVerdict::Allowed,
            ValueOrigin::External(_) if HANDLE_COPY_METHODS.contains(&method.as_str()) => {
                CallVerdict::Allowed
            }
            // A chain on an already reported call, such as client.send()?.unwrap(), is reported once.
            ValueOrigin::External(_) | ValueOrigin::Unknown
                if self.is_reported_call(&call.receiver) =>
            {
                CallVerdict::Allowed
            }
            ValueOrigin::External(certainty) => CallVerdict::External(certainty),
            ValueOrigin::FileType(owner) => self.file_type_method_verdict(&owner, &method),
            ValueOrigin::Data(data_type) => self.data_method_verdict(&data_type, &method),
            ValueOrigin::Unknown => CallVerdict::External(Certainty::Possible),
        }
    }

    /// Classifies a callee that is not a closure; visit_expr_call and is_reported_call share it.
    fn callee_verdict(&mut self, callee: &Expr) -> CallVerdict {
        match callee {
            Expr::Path(function) => match &function.qself {
                None => self.path_call_verdict(&function.path),
                // A UFCS call, such as <String as Clone>::clone, runs the trait method of the named type.
                Some(qself) => match self.path_call_verdict(&function.path) {
                    CallVerdict::Allowed => {
                        match self.finder.source.type_origin(&qself.ty, &self.scope, 0) {
                            ValueOrigin::PureStandardLibrary | ValueOrigin::DexSdk => {
                                CallVerdict::Allowed
                            }
                            _ => CallVerdict::External(Certainty::Possible),
                        }
                    }
                    verdict => verdict,
                },
            },
            // A field, index, or returned callable is a function value, so the call is dynamic.
            _ => CallVerdict::External(Certainty::Definite),
        }
    }

    fn is_reported_call(&mut self, expression: &Expr) -> bool {
        let verdict = match peel(expression) {
            Expr::MethodCall(call) => self.method_call_verdict(call),
            Expr::Call(call) => match peel(&call.func) {
                Expr::Closure(_) => CallVerdict::Allowed,
                callee => self.callee_verdict(callee),
            },
            _ => CallVerdict::Allowed,
        };
        !matches!(verdict, CallVerdict::Allowed)
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
        // Printing writes locally and reaches no external system, so it stays uncertain.
        if PRINT_MACROS.contains(&name) {
            self.add(
                value.span(),
                callee,
                CallVerdict::External(Certainty::Possible),
            );
            return;
        }
        if !PURE_MACROS.contains(&name)
            && !LOG_MACROS.contains(&name)
            && !WRITE_MACROS.contains(&name)
        {
            self.add(
                value.span(),
                callee,
                CallVerdict::External(Certainty::Possible),
            );
            return;
        }
        let Some(arguments) = macro_argument_expressions(value.tokens.clone()) else {
            let unreadable = format!("{name}! with arguments dexcli cannot read");
            self.add(
                value.span(),
                unreadable,
                CallVerdict::External(Certainty::Possible),
            );
            return;
        };
        if WRITE_MACROS.contains(&name)
            && !arguments
                .first()
                .is_some_and(|target| self.is_string_writer(target))
        {
            self.add(
                value.span(),
                callee,
                CallVerdict::External(Certainty::Possible),
            );
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

    /// A helper scan stops at the first definite external call.
    fn is_done(&self) -> bool {
        self.first_only
            && self
                .calls
                .iter()
                .any(|call| call.certainty == Certainty::Definite)
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
            Expr::Closure(_) => self.visit_expr(&call.func),
            callee => {
                let verdict = self.callee_verdict(callee);
                self.add(call.span(), source_text(&call.func), verdict);
                if !matches!(callee, Expr::Path(_)) {
                    self.visit_expr(&call.func);
                }
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

pub(crate) fn path_segments(path: &syn::Path) -> Vec<String> {
    path.segments
        .iter()
        .map(|segment| segment.ident.to_string())
        .collect()
}

/// Returns the type arguments of a path's last segment, such as T in `Vec<T>`.
fn type_arguments(path: &syn::Path) -> Vec<&Type> {
    let Some(syn::PathArguments::AngleBracketed(arguments)) =
        path.segments.last().map(|segment| &segment.arguments)
    else {
        return Vec::new();
    };
    arguments
        .args
        .iter()
        .filter_map(|argument| match argument {
            syn::GenericArgument::Type(argument) => Some(argument),
            _ => None,
        })
        .collect()
}
