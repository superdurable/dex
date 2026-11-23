// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Parse Rust workflow source for `dexcli visualize`.

mod expressions;
mod handlers;
mod metadata;
mod waits;

use std::collections::{BTreeMap, BTreeSet};
use std::error::Error;
use std::io::{stdin, stdout};

use proc_macro2::Span;
use serde_json::{Value, json};
use syn::{Expr, ImplItem, ImplItemFn, Item, ItemImpl, ItemStruct, Type, spanned::Spanned};

use expressions::{
    ExpressionCalls, associated_call, expression_path, literal_string, peel, type_name,
};

fn main() -> Result<(), Box<dyn Error>> {
    let request: Value = serde_json::from_reader(stdin().lock())?;
    let path = request["path"].as_str().ok_or("missing path")?;
    let source = request["source"].as_str().ok_or("missing source")?;
    let requested_flow = request["flowType"].as_str().unwrap_or_default();
    let mut graph = RustFlowGraph::new(path);
    match syn::parse_file(source) {
        Ok(file) => RustFlowSource::new(source, &file.items).analyze(requested_flow, &mut graph),
        Err(error) => graph.diagnostic(
            "error",
            "rust_parse_failed",
            error.to_string(),
            Some(error.span()),
        ),
    }
    serde_json::to_writer(stdout().lock(), &graph.analysis)?;
    Ok(())
}

struct RustFlowGraph {
    analysis: Value,
    node_ids: BTreeSet<String>,
}

impl RustFlowGraph {
    fn new(path: &str) -> Self {
        Self {
            analysis: json!({
                "graph": {"schemaVersion": "2.0", "valid": true, "source": {"language": "rust", "path": path},
                          "flow": {"name": ""}, "nodes": [], "edges": [], "diagnostics": []},
                "annotations": [], "attributes": [], "viewRPCs": []
            }),
            node_ids: BTreeSet::new(),
        }
    }

    fn node(&mut self, id: String, kind: &str, name: &str, location: Span, details: Value) {
        if !self.node_ids.insert(id.clone()) {
            return;
        }
        let mut node = json!({"id": id, "kind": kind, "name": name, "span": source_span(location)});
        node.as_object_mut().expect("node is an object").extend(
            details
                .as_object()
                .expect("node details are an object")
                .clone(),
        );
        self.graph_items("nodes").push(node);
    }

    fn edge(
        &mut self,
        kind: &str,
        from: &str,
        to: &str,
        label: &str,
        location: Span,
        metadata: Value,
    ) {
        self.graph_items("edges").push(json!({
            "kind": kind, "from": from, "to": to, "label": label,
            "span": source_span(location), "metadata": metadata
        }));
    }

    fn diagnostic(&mut self, severity: &str, code: &str, message: String, location: Option<Span>) {
        self.graph_items("diagnostics").push(json!({
            "severity": severity, "code": code, "message": message, "span": location.map(source_span)
        }));
        if severity == "error" {
            self.analysis["graph"]["valid"] = json!(false);
        }
    }

    fn graph_items(&mut self, key: &str) -> &mut Vec<Value> {
        self.analysis["graph"][key]
            .as_array_mut()
            .expect("graph field is an array")
    }

    fn facts(&mut self, key: &str) -> &mut Vec<Value> {
        self.analysis[key]
            .as_array_mut()
            .expect("analysis field is an array")
    }
}

struct RustFlowSource<'source> {
    source: &'source str,
    declarations: BTreeMap<String, &'source Item>,
    implementations: Vec<&'source ItemImpl>,
    flows: Vec<WorkflowImplementation<'source>>,
    steps: BTreeMap<String, WorkflowImplementation<'source>>,
    resources: BTreeMap<String, metadata::PersistenceResource>,
    rpc_definitions: BTreeMap<String, &'source Expr>,
}

struct WorkflowImplementation<'source> {
    name: String,
    rust_type: String,
    declaration: &'source ItemImpl,
}

impl<'source> RustFlowSource<'source> {
    fn new(source: &'source str, items: &'source [Item]) -> Self {
        let mut workflow = Self {
            source,
            declarations: BTreeMap::new(),
            implementations: Vec::new(),
            flows: Vec::new(),
            steps: BTreeMap::new(),
            resources: BTreeMap::new(),
            rpc_definitions: BTreeMap::new(),
        };
        workflow.index_items(items);
        workflow
    }

    fn index_items(&mut self, items: &'source [Item]) {
        for item in items {
            match item {
                Item::Impl(implementation) => self.index_implementation(implementation),
                Item::Mod(module) => {
                    if let Some((_, items)) = &module.content {
                        self.index_items(items);
                    }
                }
                Item::Struct(item_struct) => {
                    self.declarations
                        .insert(item_struct.ident.to_string(), item);
                }
                Item::Enum(item_enum) => {
                    self.declarations.insert(item_enum.ident.to_string(), item);
                }
                Item::Type(alias) => {
                    self.declarations.insert(alias.ident.to_string(), item);
                }
                Item::Fn(function) => {
                    self.declarations
                        .insert(function.sig.ident.to_string(), item);
                }
                Item::Static(resource) => {
                    self.index_resource(
                        &resource.ident.to_string(),
                        &resource.ty,
                        &resource.expr,
                        item.span(),
                    );
                }
                Item::Const(constant) => {
                    self.index_resource(
                        &constant.ident.to_string(),
                        &constant.ty,
                        &constant.expr,
                        item.span(),
                    );
                }
                _ => {}
            }
        }
    }

    fn index_implementation(&mut self, implementation: &'source ItemImpl) {
        self.implementations.push(implementation);
        let Some((None, trait_path, _)) = &implementation.trait_ else {
            return;
        };
        let Some(owner) = type_name(&implementation.self_ty) else {
            return;
        };
        let Some(trait_name) = trait_path
            .segments
            .last()
            .map(|segment| segment.ident.to_string())
        else {
            return;
        };
        let method = match trait_name.as_str() {
            "Flow" => "flow_type",
            "Step" => "step_type",
            _ => return,
        };
        let name = impl_method(implementation, method)
            .and_then(|method| {
                ExpressionCalls::new(&method.block)
                    .return_expressions()
                    .into_iter()
                    .next()
            })
            .and_then(literal_string)
            .unwrap_or_else(|| owner.clone());
        let definition = WorkflowImplementation {
            name,
            rust_type: owner.clone(),
            declaration: implementation,
        };
        if trait_name == "Flow" {
            self.flows.push(definition);
        } else {
            self.steps.insert(owner, definition);
        }
    }

    fn analyze(&self, requested: &str, graph: &mut RustFlowGraph) {
        let Some(flow) = self.select_flow(requested, graph) else {
            return;
        };
        graph.analysis["graph"]["flow"] =
            json!({"name": flow.name, "span": source_span(flow.declaration.span())});
        self.add_persistence(flow, graph);
        let registered = self.registered_steps(flow, graph);
        for rust_type in registered {
            if let Some(step) = self.steps.get(&rust_type) {
                self.analyze_step(step, graph);
            }
        }
        self.analyze_flow_handlers(flow, graph);
    }

    fn select_flow(
        &self,
        requested: &str,
        graph: &mut RustFlowGraph,
    ) -> Option<&WorkflowImplementation<'source>> {
        let matches: Vec<_> = self
            .flows
            .iter()
            .filter(|flow| requested.is_empty() || flow.name == requested)
            .collect();
        if let [flow] = matches.as_slice() {
            return Some(flow);
        }
        let available = self
            .flows
            .iter()
            .map(|flow| flow.name.as_str())
            .collect::<Vec<_>>()
            .join(", ");
        let (code, message) = match (requested.is_empty(), matches.is_empty()) {
            (true, false) => (
                "multiple_flows",
                format!("source defines multiple Flows; use --flow with one of: {available}"),
            ),
            (false, false) => (
                "multiple_flows",
                format!(
                    "multiple Flows have type {requested:?}; available Flow types: {available}"
                ),
            ),
            (true, true) => (
                "flow_not_found",
                "source must define exactly one Flow".to_owned(),
            ),
            (false, true) => {
                let available = if available.is_empty() {
                    "(none)"
                } else {
                    &available
                };
                (
                    "flow_not_found",
                    format!(
                        "Flow type {requested:?} was not found; available Flow types: {available}"
                    ),
                )
            }
        };
        graph.diagnostic("error", code, message, None);
        for flow in &self.flows {
            graph.node(
                format!("unknown:flow:{}", flow.rust_type),
                "unknown",
                &flow.name,
                flow.declaration.span(),
                json!({}),
            );
        }
        None
    }

    fn registered_steps(
        &self,
        flow: &WorkflowImplementation<'source>,
        graph: &mut RustFlowGraph,
    ) -> BTreeSet<String> {
        let Some(method) = impl_method(flow.declaration, "steps") else {
            return BTreeSet::new();
        };
        let expressions = ExpressionCalls::new(&method.block);
        let mut registered = BTreeSet::new();
        for expression in &expressions.calls {
            let is_start = associated_call(expression, "StepList", "start");
            if !is_start && !is_step_list_registration(expression, &expressions) {
                continue;
            }
            let Some(argument) =
                expressions::arguments(expression).and_then(|arguments| arguments.first())
            else {
                continue;
            };
            let Some(rust_type) = self.instance_type(&flow.rust_type, argument, &expressions, 0)
            else {
                self.target_step(&flow.rust_type, argument, &expressions, graph);
                continue;
            };
            let Some(step) = self.steps.get(&rust_type) else {
                self.target_step(&flow.rust_type, argument, &expressions, graph);
                continue;
            };
            let (location, directives) = self.step_directives(step);
            let id = format!("step:{}", step.name);
            graph.node(id.clone(), "step", &step.name, location, json!({
                "start": is_start, "phase": if impl_method(step.declaration, "wait_for").is_some() { "wait_for + execute" } else { "execute" }
            }));
            if registered.insert(rust_type) {
                graph.facts("annotations").push(json!({
                    "stepId": id, "span": source_span(location),
                    "directives": directives.into_iter().map(|(text, _)| text).collect::<Vec<_>>()
                }));
            }
            if is_start {
                if graph.analysis["graph"]["flow"].get("startStepId").is_some() {
                    graph.diagnostic(
                        "error",
                        "multiple_start_steps",
                        "Flow registers more than one Start Step".to_owned(),
                        Some(expression.span()),
                    );
                }
                graph.analysis["graph"]["flow"]["startStepId"] = json!(id);
                self.start_input(flow, step, graph);
            }
        }
        registered
    }

    fn step_directives(
        &self,
        step: &WorkflowImplementation<'source>,
    ) -> (Span, Vec<(&str, usize)>) {
        let impl_location = step.declaration.span();
        if let Some(declaration) = self.declarations.get(&step.rust_type) {
            let location = declaration.span();
            let directives = self.directives(location);
            if directives.iter().any(|(text, _)| {
                text.starts_with("dex:group") || text.starts_with("dex:explanation")
            }) {
                return (location, directives);
            }
        }
        (impl_location, self.directives(impl_location))
    }

    fn target_step(
        &self,
        owner: &str,
        expression: &Expr,
        calls: &ExpressionCalls<'_>,
        graph: &mut RustFlowGraph,
    ) -> String {
        let location = expression.span();
        if let Some(name) = self.instance_type(owner, expression, calls, 0) {
            if let Some(step) = self.steps.get(&name) {
                return format!("step:{}", step.name);
            }
            let id = format!("step:{name}");
            if !graph.node_ids.contains(&id) {
                graph.diagnostic(
                    "warning",
                    "step_type_unresolved",
                    format!("Step type {name} is not declared in this file"),
                    Some(location),
                );
                graph.node(
                    id.clone(),
                    "step",
                    &name,
                    location,
                    json!({"external": true}),
                );
            }
            return id;
        }
        graph.diagnostic(
            "error",
            "dynamic_step_target",
            "Step target must be a statically known instance".to_owned(),
            Some(location),
        );
        let id = located_id("unknown:step", location);
        graph.node(
            id.clone(),
            "unknown",
            "Dynamic Step target",
            location,
            json!({}),
        );
        id
    }

    fn instance_type(
        &self,
        owner: &str,
        expression: &Expr,
        calls: &ExpressionCalls<'_>,
        depth: usize,
    ) -> Option<String> {
        if depth >= 16 {
            return None;
        }
        match peel(expression) {
            Expr::Struct(value) => value
                .path
                .segments
                .last()
                .map(|segment| segment.ident.to_string()),
            Expr::Call(_) => {
                let (class, method) = expressions::associated_name(expression)?;
                matches!(method.as_str(), "new" | "default").then(|| {
                    if class == "Self" {
                        owner.to_owned()
                    } else {
                        class
                    }
                })
            }
            Expr::Path(value) => {
                let name = value.path.segments.last()?.ident.to_string();
                if let Some(local) = calls.locals.get(&name) {
                    return self.instance_type(owner, local, calls, depth + 1);
                }
                if matches!(name.as_str(), "self" | "Self") {
                    return Some(owner.to_owned());
                }
                let qualified = value.path.segments.len() > 1;
                let type_case = name.chars().next().is_some_and(char::is_uppercase);
                (qualified || type_case).then_some(name)
            }
            Expr::Field(field) if expression_path(&field.base).as_deref() == Some("self") => {
                let Item::Struct(ItemStruct { fields, .. }) = self.declarations.get(owner)? else {
                    return None;
                };
                match &field.member {
                    syn::Member::Named(name) => type_name(
                        &fields
                            .iter()
                            .find(|field| field.ident.as_ref() == Some(name))?
                            .ty,
                    ),
                    syn::Member::Unnamed(index) => {
                        type_name(&fields.iter().nth(index.index as usize)?.ty)
                    }
                }
            }
            Expr::MethodCall(call) if call.method == "clone" => {
                self.instance_type(owner, &call.receiver, calls, depth + 1)
            }
            _ => None,
        }
    }

    fn directives(&self, location: Span) -> Vec<(&str, usize)> {
        let preceding: Vec<_> = self
            .source
            .lines()
            .take(location.start().line.saturating_sub(1))
            .collect();
        let mut directives: Vec<_> = preceding
            .into_iter()
            .enumerate()
            .rev()
            .map_while(|(index, line)| {
                let text = line
                    .trim_start()
                    .strip_prefix("//")?
                    .trim_start_matches('/')
                    .trim();
                Some((text, index + 1))
            })
            .filter(|(text, _)| text.starts_with("dex:"))
            .collect();
        directives.reverse();
        directives
    }

    fn directive_facts(&self, location: Span, prefix: &str) -> Vec<Value> {
        self.directives(location).into_iter().filter(|(text, _)| text.starts_with(prefix)).map(|(text, line)| {
            let source = self.source.lines().nth(line - 1).unwrap_or_default();
            json!({"text": text, "span": {"startLine": line, "endLine": line,
                "startColumn": source.len() - source.trim_start().len() + 1, "endColumn": source.len() + 1}})
        }).collect()
    }

    fn source_text(&self, location: Span) -> String {
        location
            .source_text()
            .unwrap_or_default()
            .split_whitespace()
            .collect::<Vec<_>>()
            .join(" ")
    }
}

fn is_step_list_registration<'source>(
    expression: &'source Expr,
    expressions: &ExpressionCalls<'source>,
) -> bool {
    let mut current = expression;
    for _ in 0..16 {
        let Expr::MethodCall(call) = current else {
            return false;
        };
        if call.method != "and" {
            return false;
        }
        current = expressions.resolve(&call.receiver);
        if associated_call(current, "StepList", "start")
            || associated_call(current, "StepList", "empty")
        {
            return true;
        }
    }
    false
}

fn impl_method<'source>(
    implementation: &'source ItemImpl,
    name: &str,
) -> Option<&'source ImplItemFn> {
    implementation.items.iter().find_map(|item| match item {
        ImplItem::Fn(method) if method.sig.ident == name => Some(method),
        _ => None,
    })
}

fn associated_type<'source>(
    implementation: &'source ItemImpl,
    name: &str,
) -> Option<&'source Type> {
    implementation.items.iter().find_map(|item| match item {
        ImplItem::Type(declaration) if declaration.ident == name => Some(&declaration.ty),
        _ => None,
    })
}

fn located_id(prefix: &str, location: Span) -> String {
    let start = location.start();
    format!("{prefix}:{}:{}", start.line, start.column + 1)
}

fn source_span(location: Span) -> Value {
    let start = location.start();
    let end = location.end();
    json!({"startLine": start.line, "startColumn": start.column + 1,
           "endLine": end.line, "endColumn": end.column + 1})
}
