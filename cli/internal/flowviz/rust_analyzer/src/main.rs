// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Parse Rust workflow source for `dexcli visualize`.

mod expressions;
mod external_call_macros;
mod external_call_origins;
mod external_call_targets;
mod external_calls;
mod handlers;
mod metadata;
mod waits;

use std::collections::{BTreeMap, BTreeSet};
use std::error::Error;
use std::io::{stdin, stdout};

use proc_macro2::Span;
use serde_json::{Value, json};
use syn::{
    Expr, ImplItem, ImplItemFn, Item, ItemImpl, ItemStruct, Type, UseTree,
    spanned::Spanned,
    visit::{self, Visit},
};

use expressions::{
    ExpressionCalls, associated_call, expression_path, literal_string, peel, type_name,
};

fn main() -> Result<(), Box<dyn Error>> {
    let request: Value = serde_json::from_reader(stdin().lock())?;
    let path = request["path"].as_str().ok_or("missing path")?;
    let source = request["source"].as_str().ok_or("missing source")?;
    let requested_flow = request["flowType"].as_str().unwrap_or_default();
    let dex_sdk_crate_aliases: Vec<String> = request["dexSdkCrateAliases"]
        .as_array()
        .map(|aliases| {
            aliases
                .iter()
                .filter_map(|alias| alias.as_str().map(str::to_owned))
                .collect()
        })
        .unwrap_or_default();
    let mut graph = RustFlowGraph::new(path);
    match syn::parse_file(source) {
        Ok(file) => {
            // Any use or item that binds the alias, even inside a block, may shadow the Cargo rename.
            let mut local_names = LocalNameCollector::default();
            local_names.visit_file(&file);
            let dex_sdk_crate_aliases = dex_sdk_crate_aliases
                .into_iter()
                .filter(|alias| !local_names.names.contains(alias))
                .collect();
            RustFlowSource::new(source, &file.items, dex_sdk_crate_aliases)
                .analyze(requested_flow, &mut graph);
        }
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

/// Collects every name a `use` binds and every item name, anywhere in the file.
#[derive(Default)]
struct LocalNameCollector {
    names: BTreeSet<String>,
}

impl<'ast> Visit<'ast> for LocalNameCollector {
    fn visit_use_name(&mut self, name: &'ast syn::UseName) {
        self.names.insert(name.ident.to_string());
    }

    fn visit_use_rename(&mut self, rename: &'ast syn::UseRename) {
        self.names.insert(rename.rename.to_string());
    }

    fn visit_item(&mut self, item: &'ast Item) {
        let name = match item {
            Item::Mod(module) => Some(&module.ident),
            Item::Struct(declaration) => Some(&declaration.ident),
            Item::Enum(declaration) => Some(&declaration.ident),
            Item::Fn(function) => Some(&function.sig.ident),
            Item::Const(constant) => Some(&constant.ident),
            Item::Static(value) => Some(&value.ident),
            Item::Type(alias) => Some(&alias.ident),
            Item::Trait(declaration) => Some(&declaration.ident),
            _ => None,
        };
        if let Some(name) = name {
            self.names.insert(name.to_string());
        }
        visit::visit_item(self, item);
    }
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
                "annotations": [], "attributes": [], "viewRPCs": [], "methodExternalCalls": []
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
    lines: Vec<&'source str>,
    declarations: BTreeMap<String, &'source Item>,
    constants: BTreeMap<String, (&'source Type, &'source Expr)>,
    import_aliases: BTreeMap<String, String>,
    /// Full use paths by the local name they bring into scope.
    use_paths: BTreeMap<String, Vec<String>>,
    glob_imports: Vec<Vec<String>>,
    /// Names under which the package's Cargo.toml depends on dex-sdk, such as `dex`.
    dex_sdk_crate_aliases: Vec<String>,
    attribute_lines: BTreeSet<usize>,
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
    unresolved_name: Option<Span>,
}

impl<'source> RustFlowSource<'source> {
    fn new(
        source: &'source str,
        items: &'source [Item],
        dex_sdk_crate_aliases: Vec<String>,
    ) -> Self {
        let mut workflow = Self {
            lines: source.lines().collect(),
            declarations: BTreeMap::new(),
            constants: BTreeMap::new(),
            import_aliases: BTreeMap::new(),
            use_paths: BTreeMap::new(),
            glob_imports: Vec::new(),
            dex_sdk_crate_aliases,
            attribute_lines: BTreeSet::new(),
            implementations: Vec::new(),
            flows: Vec::new(),
            steps: BTreeMap::new(),
            resources: BTreeMap::new(),
            rpc_definitions: BTreeMap::new(),
        };
        workflow.index_items(items);
        for item in items {
            workflow.visit_item(item);
        }
        workflow.index_implementations(items);
        workflow
    }

    fn index_items(&mut self, items: &'source [Item]) {
        for item in items {
            match item {
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
                    self.index_constant(
                        resource.ident.to_string(),
                        &resource.ty,
                        &resource.expr,
                        item.span(),
                    );
                }
                Item::Const(constant) => {
                    self.index_constant(
                        constant.ident.to_string(),
                        &constant.ty,
                        &constant.expr,
                        item.span(),
                    );
                }
                Item::Use(import) => {
                    self.index_import_aliases(&import.tree);
                    self.index_use_paths(&import.tree, &mut Vec::new());
                }
                _ => {}
            }
        }
    }

    fn index_constant(
        &mut self,
        name: String,
        declaration: &'source Type,
        value: &'source Expr,
        location: Span,
    ) {
        self.index_resource(&name, declaration, value, location);
        self.constants.insert(name, (declaration, value));
    }

    fn index_import_aliases(&mut self, tree: &UseTree) {
        match tree {
            UseTree::Path(path) => self.index_import_aliases(&path.tree),
            UseTree::Group(group) => {
                for child in &group.items {
                    self.index_import_aliases(child);
                }
            }
            UseTree::Rename(alias) => {
                self.import_aliases
                    .insert(alias.rename.to_string(), alias.ident.to_string());
            }
            _ => {}
        }
    }

    fn imported_name<'name>(&'name self, name: &'name str) -> &'name str {
        let mut original = name;
        for _ in 0..16 {
            let Some(next) = self.import_aliases.get(original) else {
                break;
            };
            original = next;
        }
        original
    }

    fn file_symbol_name(&self, expression: &Expr) -> Option<String> {
        let Expr::Path(symbol) = peel(expression) else {
            return None;
        };
        let mut segments = symbol.path.segments.iter();
        let first = segments.next()?;
        let name = match segments.next() {
            None => &first.ident,
            Some(second)
                if (first.ident == "self" || first.ident == "crate")
                    && segments.next().is_none() =>
            {
                &second.ident
            }
            _ => return None,
        };
        Some(self.imported_name(&name.to_string()).to_owned())
    }

    fn index_implementations(&mut self, items: &'source [Item]) {
        for item in items {
            match item {
                Item::Impl(implementation) => self.index_implementation(implementation),
                Item::Mod(module) if let Some((_, items)) = &module.content => {
                    self.index_implementations(items)
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
        let name_method = impl_method(implementation, method);
        let name = name_method.and_then(|method| self.workflow_type_name(&method.block));
        let unresolved_name = if name.is_none() {
            name_method.map(Spanned::span)
        } else {
            None
        };
        let definition = WorkflowImplementation {
            name: name.unwrap_or_else(|| owner.clone()),
            rust_type: owner.clone(),
            declaration: implementation,
            unresolved_name,
        };
        if trait_name == "Flow" {
            self.flows.push(definition);
        } else {
            self.steps.insert(owner, definition);
        }
    }

    fn workflow_type_name(&self, block: &syn::Block) -> Option<String> {
        let expressions = ExpressionCalls::new(block);
        let mut names = expressions
            .conditional_returns()
            .map(|(expression, _)| self.declared_string(expressions.resolve(expression), 0));
        let name = names.next()??;
        names
            .all(|other| other.as_deref() == Some(&name))
            .then_some(name)
    }

    fn declared_string(&self, expression: &Expr, depth: usize) -> Option<String> {
        if depth >= 16 {
            return None;
        }
        if let Some(value) = literal_string(expression) {
            return Some(value);
        }
        let name = self.file_symbol_name(expression)?;
        let (_, initializer) = self.constants.get(&name)?;
        self.declared_string(initializer, depth + 1)
    }

    fn analyze(&self, requested: &str, graph: &mut RustFlowGraph) {
        let Some(flow) = self.select_flow(requested, graph) else {
            return;
        };
        graph.analysis["graph"]["flow"] =
            json!({"name": flow.name, "span": source_span(flow.declaration.span())});
        flow.report_unresolved_name("flow_type", graph);
        self.add_persistence(flow, graph);
        let registered = self.registered_steps(flow, graph);
        for rust_type in &registered {
            if let Some(step) = self.steps.get(rust_type) {
                step.report_unresolved_name("step_type", graph);
                self.analyze_step(step, graph);
            }
        }
        self.analyze_flow_handlers(flow, graph);
        self.record_method_external_calls(&registered, graph);
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
            flow.report_unresolved_name("flow_type", graph);
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
        Self::dynamic_step_target(location, graph)
    }

    fn dynamic_step_target(location: Span, graph: &mut RustFlowGraph) -> String {
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
                .map(|segment| self.instance_name(owner, &segment.ident.to_string())),
            Expr::Call(_) => {
                let (class, method) = expressions::associated_name(peel(expression))?;
                let class = self.instance_name(owner, &class);
                if matches!(class.as_str(), "Box" | "Arc" | "Rc") && method == "new" {
                    let value = expressions::arguments(expression)?.first()?;
                    return self.instance_type(owner, value, calls, depth + 1);
                }
                (matches!(method.as_str(), "new" | "default") && self.is_declared_type(&class))
                    .then_some(class)
            }
            Expr::Path(value) => {
                let bare_name = value.path.get_ident().map(ToString::to_string);
                if let Some(local) = bare_name.as_ref().and_then(|name| calls.locals.get(name)) {
                    return self.instance_type(owner, local, calls, depth + 1);
                }
                let name = self.file_symbol_name(expression)?;
                let name = self.instance_name(owner, &name);
                match self.constants.get(&name) {
                    Some((declaration, _)) => {
                        self.declared_instance_type(owner, declaration, depth + 1)
                    }
                    _ => (name == owner || self.is_declared_type(&name)).then_some(name),
                }
            }
            Expr::Field(field) if expression_path(&field.base).as_deref() == Some("self") => {
                let Item::Struct(ItemStruct { fields, .. }) = self.declarations.get(owner)? else {
                    return None;
                };
                let field = match &field.member {
                    syn::Member::Named(name) => fields
                        .iter()
                        .find(|field| field.ident.as_ref() == Some(name))?,
                    syn::Member::Unnamed(index) => fields.iter().nth(index.index as usize)?,
                };
                self.declared_instance_type(owner, &field.ty, depth + 1)
            }
            Expr::MethodCall(call) if call.method == "clone" => {
                self.instance_type(owner, &call.receiver, calls, depth + 1)
            }
            _ => None,
        }
    }

    fn is_declared_type(&self, name: &str) -> bool {
        matches!(
            self.declarations.get(name),
            Some(Item::Struct(_) | Item::Enum(_) | Item::Type(_))
        )
    }

    fn instance_name(&self, owner: &str, name: &str) -> String {
        match name {
            "self" | "Self" => owner.to_owned(),
            _ => self.imported_name(name).to_owned(),
        }
    }

    fn declared_instance_type(
        &self,
        owner: &str,
        declaration: &Type,
        depth: usize,
    ) -> Option<String> {
        if depth >= 16 {
            return None;
        }
        if let Type::Reference(reference) = declaration {
            return self.declared_instance_type(owner, &reference.elem, depth + 1);
        }
        let name = self.instance_name(owner, &type_name(declaration)?);
        if let Some(Item::Type(alias)) = self.declarations.get(&name) {
            return self.declared_instance_type(owner, &alias.ty, depth + 1);
        }
        if matches!(name.as_str(), "Box" | "Arc" | "Rc") {
            let inner = metadata::generic_types(declaration).first().copied()?;
            return self.declared_instance_type(owner, inner, depth + 1);
        }
        Some(name)
    }

    fn directives(&self, location: Span) -> Vec<(&str, usize)> {
        let mut item_line = location.start().line.saturating_sub(1);
        while item_line + 1 < location.end().line && self.is_directive_region(item_line) {
            item_line += 1;
        }
        let mut directives = Vec::new();
        for index in (0..item_line).rev() {
            if !self.is_directive_region(index) {
                break;
            }
            if let Some(text) = self.lines[index]
                .trim_start()
                .strip_prefix("//")
                .map(str::trim)
                && text.starts_with("dex:")
            {
                directives.push((text, index + 1));
            }
        }
        directives.reverse();
        directives
    }

    fn is_directive_region(&self, index: usize) -> bool {
        let line = self.lines[index].trim();
        line.is_empty() || line.starts_with("//") || self.attribute_lines.contains(&(index + 1))
    }

    fn directive_facts(&self, location: Span, prefix: &str) -> Vec<Value> {
        self.directives(location).into_iter().filter(|(text, _)| text.starts_with(prefix)).map(|(text, line)| {
            let source = self.lines[line - 1];
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

impl<'source> Visit<'source> for RustFlowSource<'source> {
    fn visit_attribute(&mut self, attribute: &'source syn::Attribute) {
        let span = attribute.span();
        let end = span.end();
        let trailing_text: String = self.lines[end.line - 1].chars().skip(end.column).collect();
        let trailing_text = trailing_text.trim_start();
        let last_attribute_line = if trailing_text.is_empty() || trailing_text.starts_with("//") {
            end.line
        } else {
            end.line - 1
        };
        self.attribute_lines
            .extend(span.start().line..=last_attribute_line);
        visit::visit_attribute(self, attribute);
    }
}

impl WorkflowImplementation<'_> {
    fn report_unresolved_name(&self, method: &str, graph: &mut RustFlowGraph) {
        if let Some(location) = self.unresolved_name {
            graph.diagnostic(
                "warning",
                &format!("{method}_unresolved"),
                format!("Cannot resolve {method}() for {}", self.rust_type),
                Some(location),
            );
        }
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
