// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use serde_json::{Value, json};
use syn::{Expr, spanned::Spanned};

use crate::expressions::{
    arguments, associated_name, block_tail, expression_path, macro_arguments,
};
use crate::handlers::{HandlerExpressions, expression_elements, returns_only_error};
use crate::{RustFlowGraph, RustFlowSource, located_id, source_span};

impl RustFlowSource<'_> {
    pub(crate) fn analyze_waits(
        &self,
        handler: &HandlerExpressions<'_>,
        step_name: &str,
        graph: &mut RustFlowGraph,
    ) {
        let outcomes = self.handler_outcomes(handler);
        let waits: Vec<_> = outcomes
            .iter()
            .filter_map(|outcome| {
                let (owner, name) = associated_name(outcome.expression)?;
                if owner != "Wait" {
                    return None;
                }
                let kind = match name.as_str() {
                    "skip_immediately" => "skipWaitImmediately",
                    "until" => "until",
                    "all_of" => "allOf",
                    "any_of" => "anyOf",
                    "any_combination_of" => "anyComboOf",
                    _ => return None,
                };
                Some((outcome, kind))
            })
            .collect();
        if waits.is_empty() {
            let location = handler.block.span();
            if returns_only_error(&outcomes) {
                graph.node(format!("{}:0", located_id(&format!("wait:{step_name}"), location)), "wait", "failure", location,
                    json!({"parentId": handler.id, "phase": handler.phase, "wait": {"type": "failure", "conditions": []}}));
            } else {
                graph.node(
                    located_id(&format!("unknown:wait:{step_name}"), location),
                    "unknown",
                    "Dynamic WaitFor",
                    location,
                    json!({"parentId": handler.id}),
                );
                graph.diagnostic(
                    "error",
                    "hidden_dex_wait",
                    "wait_for hides its Dex Wait in a helper".to_owned(),
                    Some(location),
                );
            }
            return;
        }
        if waits.len() > 1 {
            graph.node(
                format!("wait-dispatch:{step_name}"),
                "wait_dispatch",
                "WaitFor",
                handler.block.span(),
                json!({"parentId": handler.id, "phase": handler.phase}),
            );
        }
        for (index, (outcome, kind)) in waits.into_iter().enumerate() {
            let location = outcome.expression.span();
            let id = format!(
                "{}:{index}",
                located_id(&format!("wait:{step_name}"), location)
            );
            let mut conditions = Vec::new();
            if let Some(args) = arguments(outcome.expression) {
                for expression in args {
                    self.collect_conditions(handler, expression, &id, &mut conditions, graph, 0);
                }
            }
            graph.node(
                id,
                "wait",
                kind,
                location,
                json!({
                    "parentId": handler.id, "phase": handler.phase, "condition": outcome.condition,
                    "wait": {"type": kind, "conditions": conditions}
                }),
            );
        }
    }

    fn collect_conditions(
        &self,
        handler: &HandlerExpressions<'_>,
        expression: &Expr,
        wait_id: &str,
        conditions: &mut Vec<Value>,
        graph: &mut RustFlowGraph,
        depth: usize,
    ) {
        if depth >= 24 {
            return;
        }
        if let Some(name) = expression_path(expression)
            && handler.calls.locals.contains_key(&name)
        {
            for expression in &handler.calls.calls {
                if let Expr::MethodCall(call) = expression
                    && matches!(
                        call.method.to_string().as_str(),
                        "push" | "extend" | "append"
                    )
                    && expression_path(&call.receiver).as_deref() == Some(&name)
                {
                    for argument in &call.args {
                        self.collect_conditions(
                            handler,
                            argument,
                            wait_id,
                            conditions,
                            graph,
                            depth + 1,
                        );
                    }
                }
            }
        }
        let expression = handler.calls.resolve(expression);
        if let Some(condition) = self.wait_condition(handler, expression, wait_id, graph) {
            conditions.push(condition);
            return;
        }
        let mut children = expression_elements(expression);
        match expression {
            Expr::MethodCall(call) => {
                children.push(&call.receiver);
                children.extend(&call.args);
            }
            Expr::Call(call) => children.extend(&call.args),
            Expr::Closure(closure) => children.push(&closure.body),
            Expr::If(branch) => {
                children.extend(block_tail(&branch.then_branch));
                children.extend(
                    branch
                        .else_branch
                        .iter()
                        .map(|(_, alternative)| &**alternative),
                );
            }
            Expr::Block(block) => children.extend(block_tail(&block.block)),
            Expr::Macro(expression) if expression.mac.path.is_ident("vec") => {
                if let Ok(elements) = macro_arguments(&expression.mac) {
                    for element in &elements {
                        self.collect_conditions(
                            handler,
                            element,
                            wait_id,
                            conditions,
                            graph,
                            depth + 1,
                        );
                    }
                }
            }
            _ => {}
        }
        for child in children {
            self.collect_conditions(handler, child, wait_id, conditions, graph, depth + 1);
        }
    }

    fn wait_condition(
        &self,
        handler: &HandlerExpressions<'_>,
        expression: &Expr,
        wait_id: &str,
        graph: &mut RustFlowGraph,
    ) -> Option<Value> {
        let location = expression.span();
        let span = source_span(location);
        if let Some((owner, method)) = associated_name(expression) {
            return match (owner.as_str(), method.as_str()) {
                ("Timer", "by_duration") => {
                    let duration = arguments(expression)?.first()?;
                    let expression = self.source_text(duration.span());
                    Some(
                        json!({"kind": "timer", "label": format!("{expression} timer"), "expression": expression, "span": span}),
                    )
                }
                ("SubFlow", "run" | "run_with_options") => {
                    let target = arguments(expression)?.first()?;
                    let rust_type = self
                        .instance_type(handler.owner, target, &handler.calls, 0)
                        .unwrap_or_else(|| self.source_text(target.span()));
                    let name = self
                        .flows
                        .iter()
                        .find(|flow| flow.rust_type == rust_type)
                        .map_or(rust_type.as_str(), |flow| &flow.name);
                    let id = format!("subflow:{name}:{}", location.start().line);
                    graph.node(
                        id.clone(),
                        "subflow",
                        name,
                        location,
                        json!({"external": true}),
                    );
                    graph.edge("subflow", wait_id, &id, "start", location, json!({}));
                    Some(json!({"kind": "subflow", "label": name, "subFlowId": id, "span": span}))
                }
                ("Condition", "graceful_completion_requested") => Some(json!({
                    "kind": "gracefulCompletion", "label": "Graceful completion requested", "span": span
                })),
                _ => None,
            };
        }
        let Expr::MethodCall(call) = expression else {
            return None;
        };
        let resource = self.resource(&call.receiver, &handler.calls, 0)?;
        if resource.kind.kind() != "channel" {
            return None;
        }
        let label = match call.method.to_string().as_str() {
            "for_one" => "for 1",
            "for_n" => "for N",
            "at_least" => "at least N",
            "at_most" => "at most N",
            "range" => "range",
            "for_all_branches" => "for all branches",
            "for_first_branch" => "for first branch",
            "for_branches" => "for N branches",
            "for_remaining_branches" => "for remaining branches",
            _ => return None,
        };
        let label = format!("{}.{label}", resource.key);
        graph.edge(
            "wait_condition",
            &resource.id,
            wait_id,
            &label,
            location,
            json!({}),
        );
        Some(
            json!({"kind": "channel", "label": label, "resourceId": resource.id, "expression": self.source_text(location), "span": span}),
        )
    }
}
