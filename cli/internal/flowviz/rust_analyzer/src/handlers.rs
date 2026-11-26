// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeSet;

use proc_macro2::Span;
use serde_json::json;
use syn::{Block, Expr, Item, spanned::Spanned};

use crate::expressions::{
    ExpressionCalls, arguments, associated_call, associated_name, block_tail, expression_path,
    join_predicates, literal_string, macro_arguments, match_predicate, peel, source_text,
};
use crate::metadata::ResourceKind;
use crate::metadata::rpc_output_keys;
use crate::{
    RustFlowGraph, RustFlowSource, WorkflowImplementation, impl_method, located_id, source_span,
};

pub(crate) struct HandlerExpressions<'source> {
    pub(crate) owner: &'source str,
    pub(crate) id: &'source str,
    pub(crate) phase: &'static str,
    pub(crate) block: &'source Block,
    pub(crate) calls: ExpressionCalls<'source>,
}

impl<'source> HandlerExpressions<'source> {
    fn new(
        owner: &'source str,
        id: &'source str,
        phase: &'static str,
        block: &'source Block,
    ) -> Self {
        Self {
            owner,
            id,
            phase,
            block,
            calls: ExpressionCalls::new(block),
        }
    }

    fn delegated<'call>(
        &self,
        function: &'call syn::ItemFn,
        call: &'call syn::ExprCall,
    ) -> HandlerExpressions<'call>
    where
        'source: 'call,
    {
        let mut handler = HandlerExpressions::new(self.owner, self.id, self.phase, &function.block);
        let caller: &ExpressionCalls<'call> = &self.calls;
        for (parameter, argument) in function.sig.inputs.iter().zip(&call.args) {
            if let syn::FnArg::Typed(parameter) = parameter
                && let syn::Pat::Ident(identifier) = parameter.pat.as_ref()
            {
                handler
                    .calls
                    .locals
                    .entry(identifier.ident.to_string())
                    .or_insert_with(|| caller.resolve(argument));
            }
        }
        handler
    }
}

pub(crate) struct HandlerOutcome<'source> {
    pub(crate) expression: &'source Expr,
    pub(crate) condition: String,
}

impl<'source> RustFlowSource<'source> {
    pub(crate) fn analyze_step(
        &self,
        step: &WorkflowImplementation<'source>,
        graph: &mut RustFlowGraph,
    ) {
        let id = format!("step:{}", step.name);
        if let Some(method) = impl_method(step.declaration, "execute") {
            let handler = HandlerExpressions::new(&step.rust_type, &id, "execute", &method.block);
            self.analyze_decisions(&handler, graph);
            self.analyze_resource_access(&handler, graph);
        }
        if let Some(method) = impl_method(step.declaration, "wait_for") {
            let handler = HandlerExpressions::new(&step.rust_type, &id, "wait_for", &method.block);
            self.analyze_waits(&handler, &step.name, graph);
            self.analyze_resource_access(&handler, graph);
        }
        if let Some(options) = impl_method(step.declaration, "options") {
            self.analyze_options(
                HandlerExpressions::new(&step.rust_type, &id, "execute", &options.block),
                graph,
            );
        }
    }

    fn analyze_options<'handler>(
        &self,
        mut handler: HandlerExpressions<'handler>,
        graph: &mut RustFlowGraph,
    ) where
        'source: 'handler,
    {
        let mut visited = BTreeSet::new();
        loop {
            self.analyze_execute_failure(&handler, graph);
            self.analyze_locks(&handler, graph);
            let Some(Expr::Call(call)) = block_tail(handler.block).map(peel) else {
                return;
            };
            let Some(name) = self.file_symbol_name(&call.func) else {
                return;
            };
            let Some(function) = self.file_function(&name) else {
                return;
            };
            if !visited.insert(name) {
                return;
            }
            handler = handler.delegated(function, call);
        }
    }

    pub(crate) fn analyze_flow_handlers(
        &self,
        flow: &WorkflowImplementation<'source>,
        graph: &mut RustFlowGraph,
    ) {
        if let Some(registration) = impl_method(flow.declaration, "rpcs") {
            let registrations = ExpressionCalls::new(&registration.block);
            for expression in registrations.calls.iter().rev() {
                let Expr::MethodCall(binding) = expression else {
                    continue;
                };
                if !matches!(
                    binding.method.to_string().as_str(),
                    "function" | "function_without_input" | "procedure" | "procedure_without_input"
                ) {
                    continue;
                }
                let (Some(definition), Some(method)) =
                    (binding.args.first(), binding.args.iter().nth(1))
                else {
                    continue;
                };
                self.analyze_rpc(flow, &registrations, definition, method, graph);
            }
        }
        if let Some(registration) = impl_method(flow.declaration, "timeout_handler") {
            let registrations = ExpressionCalls::new(&registration.block);
            for expression in registrations.calls {
                let Expr::Call(call) = expression else {
                    continue;
                };
                if expression_path(&call.func).as_deref() != Some("Some") {
                    continue;
                }
                let Some(method) = call.args.first().and_then(expression_path) else {
                    continue;
                };
                let Some((block, location)) = self.handler_method(&flow.rust_type, &method) else {
                    continue;
                };
                let id = format!("timeout_handler:{}", flow.name);
                graph.node(
                    id.clone(),
                    "timeout_handler",
                    "handleTimeout",
                    location,
                    json!({}),
                );
                let handler = HandlerExpressions::new(&flow.rust_type, &id, "timeout", block);
                self.analyze_decisions(&handler, graph);
                self.analyze_resource_access(&handler, graph);
            }
        }
        for implementation in &self.implementations {
            if crate::type_name(&implementation.self_ty).as_deref() != Some(&flow.rust_type) {
                continue;
            }
            for item in &implementation.items {
                let syn::ImplItem::Fn(method) = item else {
                    continue;
                };
                let calls = ExpressionCalls::new(&method.block);
                for expression in &calls.calls {
                    if !associated_name(expression).is_some_and(|(owner, name)| {
                        matches!(
                            owner.as_str(),
                            "FlowTimeoutHandlerOptions" | "FlowTimeoutHandlerFailure"
                        ) && matches!(name.as_str(), "proceed_to" | "proceed_to_with_options")
                    }) {
                        continue;
                    }
                    let Some(target) = arguments(expression).and_then(|args| args.first()) else {
                        continue;
                    };
                    let id = format!("timeout_handler:{}", flow.name);
                    graph.node(
                        id.clone(),
                        "timeout_handler",
                        "handleTimeout",
                        method.span(),
                        json!({}),
                    );
                    let target = self.target_step(&flow.rust_type, target, &calls, graph);
                    graph.edge(
                        "failure_transition",
                        &id,
                        &target,
                        "Timeout handler failure",
                        expression.span(),
                        json!({}),
                    );
                }
            }
        }
    }

    fn handler_method(&self, owner: &str, name: &str) -> Option<(&Block, Span)> {
        for implementation in &self.implementations {
            if crate::type_name(&implementation.self_ty).as_deref() == Some(owner)
                && let Some(method) = impl_method(implementation, name)
            {
                return Some((&method.block, method.span()));
            }
        }
        self.file_function(name)
            .map(|function| (function.block.as_ref(), function.span()))
    }

    fn file_function(&self, name: &str) -> Option<&'source syn::ItemFn> {
        let Item::Fn(function) = self.declarations.get(name)? else {
            return None;
        };
        Some(function)
    }

    fn analyze_rpc(
        &self,
        flow: &WorkflowImplementation<'source>,
        registrations: &ExpressionCalls<'_>,
        definition: &Expr,
        method: &Expr,
        graph: &mut RustFlowGraph,
    ) {
        let definition = registrations.resolve(definition);
        let definition_calls = ExpressionCalls::from_expression(definition);
        let protocol_name = definition_calls
            .calls
            .iter()
            .find_map(|call| {
                let candidate = if associated_call(call, "Rpc", "new") {
                    *call
                } else if let Expr::MethodCall(method) = call {
                    let symbol = expression_path(&method.receiver)?;
                    return self.rpc_protocol_name(self.rpc_definitions.get(&symbol)?);
                } else {
                    return None;
                };
                arguments(candidate)?.first().and_then(literal_string)
            })
            .or_else(|| {
                self.rpc_protocol_name(self.rpc_definitions.get(&expression_path(definition)?)?)
            });
        let (Some(protocol_name), Some(method)) = (protocol_name, expression_path(method)) else {
            return;
        };
        let id = format!("rpc:{protocol_name}");
        let implementation = self.handler_method(&flow.rust_type, &method);
        let location = implementation.map_or_else(|| definition.span(), |(_, location)| location);
        graph.node(id.clone(), "rpc", &protocol_name, location, json!({}));
        for expression in &definition_calls.calls {
            let Expr::MethodCall(call) = expression else {
                continue;
            };
            if call.method == "lock"
                && let Some(resource) = call
                    .args
                    .first()
                    .and_then(|arg| self.resource(arg, registrations, 0))
            {
                graph.edge(
                    "resource_lock",
                    &id,
                    &resource.id,
                    "lock",
                    call.span(),
                    json!({"phase": "rpc", "rpcName": protocol_name}),
                );
            }
        }
        let Some((block, _)) = implementation else {
            return;
        };
        let handler = HandlerExpressions::new(&flow.rust_type, &id, "rpc", block);
        self.analyze_decisions(&handler, graph);
        self.analyze_resource_access(&handler, graph);
        if matches!(protocol_name.as_str(), "GetDexSummary" | "GetDexDisplay") {
            let mut fact = json!({
                "protocolName": protocol_name, "span": source_span(location),
                "directives": self.directive_facts(location, "dex:field")
            });
            if let Some(keys) = rpc_output_keys(&handler.calls) {
                fact["outputKeys"] = json!(keys);
            }
            graph.facts("viewRPCs").push(fact);
        }
    }

    fn rpc_protocol_name(&self, definition: &Expr) -> Option<String> {
        ExpressionCalls::from_expression(definition)
            .calls
            .into_iter()
            .find(|call| associated_call(call, "Rpc", "new"))
            .and_then(|call| arguments(call)?.first().and_then(literal_string))
    }

    fn analyze_execute_failure(&self, handler: &HandlerExpressions<'_>, graph: &mut RustFlowGraph) {
        for expression in &handler.calls.calls {
            let Expr::MethodCall(call) = expression else {
                continue;
            };
            if call.method != "on_execute_failure_proceed_to" {
                continue;
            }
            let Some(target) = call.args.first() else {
                continue;
            };
            let type_name = self.instance_type(handler.owner, target, &handler.calls, 0);
            let skip_wait = type_name
                .and_then(|name| self.steps.get(&name))
                .is_none_or(|step| impl_method(step.declaration, "wait_for").is_none());
            let target = self.target_step(handler.owner, target, &handler.calls, graph);
            graph.edge(
                "failure_transition",
                handler.id,
                &target,
                "Execute failure",
                call.span(),
                json!({"skipWaitFor": skip_wait}),
            );
        }
    }

    fn analyze_locks(&self, handler: &HandlerExpressions<'_>, graph: &mut RustFlowGraph) {
        for expression in &handler.calls.calls {
            let Expr::MethodCall(call) = expression else {
                continue;
            };
            let phase = match call.method.to_string().as_str() {
                "execute_lock" => "execute",
                "wait_for_lock" => "wait_for",
                _ => continue,
            };
            if let Some(resource) = call
                .args
                .first()
                .and_then(|arg| self.resource(arg, &handler.calls, 0))
            {
                graph.edge(
                    "resource_lock",
                    handler.id,
                    &resource.id,
                    "lock",
                    call.span(),
                    json!({"phase": phase}),
                );
            }
        }
    }

    fn analyze_resource_access(&self, handler: &HandlerExpressions<'_>, graph: &mut RustFlowGraph) {
        let mut seen = BTreeSet::new();
        for expression in &handler.calls.calls {
            let Expr::MethodCall(call) = expression else {
                continue;
            };
            let Some(resource) = self.resource(&call.receiver, &handler.calls, 0) else {
                continue;
            };
            let (kind, label) = match call.method.to_string().as_str() {
                "get" => ("resource_read", "Get"),
                "get_required" => ("resource_read", "GetRequired"),
                "condition_results" | "branch_results" | "remaining_branch_results" => {
                    ("resource_read", "GetConditionResults")
                }
                "size"
                | "map_size"
                | "all_instance_keys"
                | "pending_messages"
                | "find_pending_message" => ("resource_read", "Read"),
                "publish" | "report_branch_result" | "end_branch" => {
                    ("resource_publish", "Publish")
                }
                "set" => ("resource_write", "Set"),
                "delete" => ("resource_write", "Delete"),
                "write" | "buffered_text" | "buffered_text_with_options"
                    if resource.kind == ResourceKind::Stream =>
                {
                    ("resource_write", "Write")
                }
                _ => continue,
            };
            if !seen.insert((&resource.id, kind, label)) {
                continue;
            }
            let (from, to) = if kind == "resource_read" {
                (resource.id.as_str(), handler.id)
            } else {
                (handler.id, resource.id.as_str())
            };
            graph.edge(
                kind,
                from,
                to,
                label,
                call.span(),
                json!({"phase": handler.phase}),
            );
        }
    }

    fn analyze_decisions(&self, handler: &HandlerExpressions<'_>, graph: &mut RustFlowGraph) {
        let outcomes = self.handler_outcomes(handler);
        let decisions: Vec<_> = outcomes
            .iter()
            .filter_map(|outcome| {
                self.decision_root(outcome.expression, handler)
                    .map(|root| (outcome, root))
            })
            .collect();
        if decisions.is_empty() && handler.phase != "rpc" && !returns_only_error(&outcomes) {
            let location = handler.block.span();
            let unknown = located_id(&format!("unknown:{}", handler.id), location);
            graph.node(
                unknown.clone(),
                "unknown",
                "Dynamic Dex decision",
                location,
                json!({}),
            );
            graph.edge(
                "transition",
                handler.id,
                &unknown,
                "dynamic",
                location,
                json!({}),
            );
            graph.diagnostic(
                "error",
                "hidden_dex_decision",
                "Handler hides its Dex decision in a helper".to_owned(),
                Some(location),
            );
            return;
        }
        if decisions.len() > 1 && handler.phase != "rpc" {
            graph.node(
                format!("decision-dispatch:{}", handler.id),
                "decision_dispatch",
                "Decision",
                handler.block.span(),
                json!({"parentId": handler.id, "phase": handler.phase}),
            );
        }
        for (outcome, (root, decision_type)) in decisions {
            self.add_decision(handler, outcome, root, decision_type, graph);
        }
    }

    fn decision_root<'expression>(
        &self,
        expression: &'expression Expr,
        handler: &HandlerExpressions<'expression>,
    ) -> Option<(&'expression Expr, &'static str)> {
        let mut current = expression;
        loop {
            let resolved = handler.calls.resolve(current);
            if let Some((owner, method)) = associated_name(resolved) {
                let decision = match (owner.as_str(), method.as_str()) {
                    ("StepDecision", "go_to") => "goTo",
                    ("StepDecision", "go_to_many") => "goToMany",
                    ("StepDecision", "graceful_complete") => "gracefulComplete",
                    ("StepDecision", "force_complete") => "forceComplete",
                    ("StepDecision", "force_complete_if_channels_empty") => {
                        "forceCompleteIfChannelsEmpty"
                    }
                    ("StepDecision", "force_fail") => "forceFail",
                    ("StepDecision", "dead_end") => "deadEnd",
                    ("RpcResult", "new") => "rpcResult",
                    _ => return None,
                };
                return Some((resolved, decision));
            }
            let Expr::MethodCall(call) = resolved else {
                return None;
            };
            if call.method == "end_branch"
                && self
                    .resource(&call.receiver, &handler.calls, 0)
                    .is_some_and(|resource| resource.kind == ResourceKind::Join)
            {
                return Some((resolved, "deadEnd"));
            }
            current = &call.receiver;
        }
    }

    fn add_decision(
        &self,
        handler: &HandlerExpressions<'_>,
        outcome: &HandlerOutcome<'_>,
        root: &Expr,
        decision_type: &str,
        graph: &mut RustFlowGraph,
    ) {
        let location = outcome.expression.span();
        let id = located_id(&format!("decision:{}", handler.id), location);
        let calls = ExpressionCalls::from_expression(outcome.expression);
        let mut cancellations = Vec::new();
        let mut transitions = BTreeSet::new();
        let mut visited_locals = BTreeSet::new();
        for expression in &calls.calls {
            let Expr::MethodCall(call) = expression else {
                continue;
            };
            if matches!(
                call.method.to_string().as_str(),
                "cancel_step" | "cancel_sibling_step"
            ) && let Some(target) = call.args.first()
            {
                let target = self.target_step(handler.owner, target, &handler.calls, graph);
                let scope = if call.method == "cancel_sibling_step" {
                    "siblings"
                } else {
                    "all"
                };
                graph.edge(
                    "cancel",
                    &id,
                    &target,
                    "Cancel",
                    call.span(),
                    json!({"scope": scope}),
                );
                cancellations.push(json!({"stepId": target, "scope": scope}));
            } else if call.method == "then"
                && let Some(movement) = call.args.first()
            {
                self.collect_movements(
                    handler,
                    movement,
                    &mut transitions,
                    &mut visited_locals,
                    graph,
                    0,
                );
            }
        }
        let mut checked_channels = BTreeSet::new();
        if let Some(args) = arguments(root) {
            if decision_type == "goTo"
                && let Some(target) = args.first()
            {
                transitions.insert(self.target_step(handler.owner, target, &handler.calls, graph));
            } else if decision_type == "goToMany"
                && let Some(movements) = args.first()
            {
                self.collect_movements(
                    handler,
                    movements,
                    &mut transitions,
                    &mut visited_locals,
                    graph,
                    0,
                );
            } else if decision_type == "forceCompleteIfChannelsEmpty" {
                if let Some(fallback) = args.iter().nth(1) {
                    self.collect_movements(
                        handler,
                        fallback,
                        &mut transitions,
                        &mut visited_locals,
                        graph,
                        0,
                    );
                }
                if let Some(channels) = args.iter().nth(2) {
                    for channel in expression_elements(handler.calls.resolve(channels)) {
                        if let Some(resource) = self.resource(channel, &handler.calls, 0) {
                            checked_channels.insert(&resource.id);
                        }
                    }
                }
            }
        }
        graph.node(id.clone(), "decision", decision_type, location, json!({
            "parentId": handler.id, "phase": handler.phase, "condition": outcome.condition,
            "decision": {"type": decision_type, "cancellations": cancellations, "checkedChannels": checked_channels}
        }));
        for target in transitions {
            graph.edge("transition", &id, &target, "", location, json!({}));
        }
    }

    fn collect_movements(
        &self,
        handler: &HandlerExpressions<'_>,
        expression: &Expr,
        targets: &mut BTreeSet<String>,
        visited_locals: &mut BTreeSet<String>,
        graph: &mut RustFlowGraph,
        depth: usize,
    ) {
        if depth >= 32 {
            return;
        }
        let expression = peel(expression);
        if let Some(name) = expression_path(expression)
            && let Some(initializer) = handler.calls.locals.get(&name)
            && visited_locals.insert(name.clone())
        {
            self.collect_movements(
                handler,
                initializer,
                targets,
                visited_locals,
                graph,
                depth + 1,
            );
            for call in &handler.calls.calls {
                if let Expr::MethodCall(call) = call
                    && matches!(
                        call.method.to_string().as_str(),
                        "push" | "extend" | "append"
                    )
                    && expression_path(&call.receiver).as_deref() == Some(&name)
                {
                    for argument in &call.args {
                        self.collect_movements(
                            handler,
                            argument,
                            targets,
                            visited_locals,
                            graph,
                            depth + 1,
                        );
                    }
                }
            }
            return;
        }
        if associated_name(expression).is_some_and(|(owner, name)| {
            owner == "StepMovement" && matches!(name.as_str(), "to" | "to_with_options")
        }) && let Some(target) = arguments(expression).and_then(|args| args.first())
        {
            targets.insert(self.target_step(handler.owner, target, &handler.calls, graph));
            return;
        }
        match expression {
            Expr::Array(array) => {
                for element in &array.elems {
                    self.collect_movements(
                        handler,
                        element,
                        targets,
                        visited_locals,
                        graph,
                        depth + 1,
                    );
                }
            }
            Expr::Call(call) => {
                if let Some(name) = self.file_symbol_name(&call.func)
                    && let Some(function) = self.file_function(&name)
                {
                    self.collect_branch_builder_targets(handler, function, call, targets, graph);
                } else if associated_name(expression).is_some_and(|(owner, method)| {
                    matches!(
                        (owner.as_str(), method.as_str()),
                        ("Box" | "Arc" | "Rc", "new") | ("Vec", "new" | "with_capacity" | "from")
                    )
                }) || expression_path(&call.func).as_deref() == Some("Some")
                {
                    for argument in &call.args {
                        self.collect_movements(
                            handler,
                            argument,
                            targets,
                            visited_locals,
                            graph,
                            depth + 1,
                        );
                    }
                } else {
                    targets.insert(Self::dynamic_step_target(call.span(), graph));
                }
            }
            Expr::MethodCall(call) => {
                self.collect_movements(
                    handler,
                    &call.receiver,
                    targets,
                    visited_locals,
                    graph,
                    depth + 1,
                );
                for argument in &call.args {
                    self.collect_movements(
                        handler,
                        argument,
                        targets,
                        visited_locals,
                        graph,
                        depth + 1,
                    );
                }
            }
            Expr::Closure(closure) => self.collect_movements(
                handler,
                &closure.body,
                targets,
                visited_locals,
                graph,
                depth + 1,
            ),
            Expr::Cast(cast) => self.collect_movements(
                handler,
                &cast.expr,
                targets,
                visited_locals,
                graph,
                depth + 1,
            ),
            Expr::Block(block) => {
                let calls = ExpressionCalls::new(&block.block);
                for expression in calls.calls {
                    self.collect_movements(
                        handler,
                        expression,
                        targets,
                        visited_locals,
                        graph,
                        depth + 1,
                    );
                }
            }
            Expr::Macro(expression) if expression.mac.path.is_ident("vec") => {
                if let Ok(elements) = macro_arguments(&expression.mac) {
                    for element in &elements {
                        self.collect_movements(
                            handler,
                            element,
                            targets,
                            visited_locals,
                            graph,
                            depth + 1,
                        );
                    }
                }
            }
            _ => {}
        }
    }

    fn collect_branch_builder_targets(
        &self,
        handler: &HandlerExpressions<'_>,
        function: &syn::ItemFn,
        call: &syn::ExprCall,
        targets: &mut BTreeSet<String>,
        graph: &mut RustFlowGraph,
    ) {
        let delegated = handler.delegated(function, call);
        for movement in &delegated.calls.calls {
            if !associated_name(movement).is_some_and(|(owner, method)| {
                owner == "StepMovement" && matches!(method.as_str(), "to" | "to_with_options")
            }) {
                continue;
            }
            let Some(target) = arguments(movement).and_then(|args| args.first()) else {
                continue;
            };
            targets.insert(self.target_step(handler.owner, target, &delegated.calls, graph));
        }
    }

    pub(crate) fn handler_outcomes<'expression>(
        &self,
        handler: &HandlerExpressions<'expression>,
    ) -> Vec<HandlerOutcome<'expression>> {
        let mut outcomes = Vec::new();
        for (expression, condition) in handler.calls.conditional_returns() {
            self.collect_outcomes(expression, condition, &handler.calls, &mut outcomes, 0);
        }
        if outcomes.len() > 1 && outcomes.iter().any(|outcome| !outcome.condition.is_empty()) {
            for outcome in &mut outcomes {
                if outcome.condition.is_empty() {
                    outcome.condition = "otherwise".to_owned();
                }
            }
        }
        outcomes
    }

    fn collect_outcomes<'expression>(
        &self,
        expression: &'expression Expr,
        condition: &str,
        calls: &ExpressionCalls<'expression>,
        outcomes: &mut Vec<HandlerOutcome<'expression>>,
        depth: usize,
    ) {
        if depth >= 24 {
            return;
        }
        let expression = calls.resolve(expression);
        match expression {
            Expr::Block(block) => {
                if let Some(tail) = block_tail(&block.block) {
                    self.collect_outcomes(
                        tail,
                        &join_predicates(condition, calls.tail_predicate(tail)),
                        calls,
                        outcomes,
                        depth + 1,
                    );
                }
            }
            Expr::If(branch) => {
                let predicate = source_text(&branch.cond);
                if let Some(tail) = block_tail(&branch.then_branch) {
                    self.collect_outcomes(
                        tail,
                        &join_predicates(
                            &join_predicates(condition, &predicate),
                            calls.tail_predicate(tail),
                        ),
                        calls,
                        outcomes,
                        depth + 1,
                    );
                }
                if let Some((_, alternative)) = &branch.else_branch {
                    self.collect_outcomes(
                        alternative,
                        &join_predicates(condition, &format!("!({predicate})")),
                        calls,
                        outcomes,
                        depth + 1,
                    );
                }
            }
            Expr::Match(branch) => {
                for arm in &branch.arms {
                    let predicate = match_predicate(&branch.expr, arm);
                    self.collect_outcomes(
                        &arm.body,
                        &join_predicates(condition, &predicate),
                        calls,
                        outcomes,
                        depth + 1,
                    );
                }
            }
            Expr::Call(call) if expression_path(&call.func).as_deref() == Some("Ok") => {
                if let Some(value) = call.args.first() {
                    self.collect_outcomes(value, condition, calls, outcomes, depth + 1);
                }
            }
            Expr::Return(_) => {}
            _ => outcomes.push(HandlerOutcome {
                expression,
                condition: condition.to_owned(),
            }),
        }
    }
}

pub(crate) fn returns_only_error(outcomes: &[HandlerOutcome<'_>]) -> bool {
    !outcomes.is_empty() && outcomes.iter().all(|outcome| {
        matches!(outcome.expression, Expr::Call(call) if expression_path(&call.func).as_deref() == Some("Err"))
    })
}

pub(crate) fn expression_elements(expression: &Expr) -> Vec<&Expr> {
    match peel(expression) {
        Expr::Array(array) => array.elems.iter().collect(),
        Expr::Tuple(tuple) => tuple.elems.iter().collect(),
        _ => Vec::new(),
    }
}
