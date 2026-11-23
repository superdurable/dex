// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;

use syn::punctuated::Punctuated;
use syn::spanned::Spanned;
use syn::visit::{self, Visit};
use syn::{Block, Expr, Local, Pat, Stmt, Token, Type, UnOp};

pub(crate) struct ExpressionCalls<'source> {
    pub(crate) calls: Vec<&'source Expr>,
    pub(crate) locals: BTreeMap<String, &'source Expr>,
    tail: Option<&'source Expr>,
    returns: Vec<(&'source Expr, String)>,
    predicates: Vec<String>,
    tail_predicates: BTreeMap<(usize, usize), String>,
}

impl<'source> ExpressionCalls<'source> {
    pub(crate) fn new(block: &'source Block) -> Self {
        let mut expressions = Self::empty();
        expressions.tail = block_tail(block);
        expressions.visit_block(block);
        expressions
    }

    pub(crate) fn from_expression(expression: &'source Expr) -> Self {
        let mut expressions = Self::empty();
        expressions.visit_expr(expression);
        expressions
    }

    fn empty() -> Self {
        Self {
            calls: Vec::new(),
            locals: BTreeMap::new(),
            tail: None,
            returns: Vec::new(),
            predicates: Vec::new(),
            tail_predicates: BTreeMap::new(),
        }
    }

    pub(crate) fn return_expressions(&self) -> Vec<&'source Expr> {
        self.conditional_returns()
            .map(|(expression, _)| expression)
            .collect()
    }

    pub(crate) fn conditional_returns(&self) -> impl Iterator<Item = (&'source Expr, &str)> {
        self.returns
            .iter()
            .map(|(expression, predicate)| (*expression, predicate.as_str()))
            .chain(self.tail.map(|tail| (tail, self.tail_predicate(tail))))
    }

    pub(crate) fn tail_predicate(&self, expression: &Expr) -> &str {
        let start = expression.span().start();
        self.tail_predicates
            .get(&(start.line, start.column))
            .map_or("", String::as_str)
    }

    pub(crate) fn resolve(&self, expression: &'source Expr) -> &'source Expr {
        let mut resolved = peel(expression);
        for _ in 0..16 {
            let Some(name) = expression_path(resolved) else {
                break;
            };
            let Some(value) = self.locals.get(&name) else {
                break;
            };
            resolved = peel(value);
        }
        resolved
    }
}

impl<'source> Visit<'source> for ExpressionCalls<'source> {
    fn visit_expr(&mut self, expression: &'source Expr) {
        if matches!(expression, Expr::Call(_) | Expr::MethodCall(_)) {
            self.calls.push(expression);
        }
        if let Expr::Return(value) = expression
            && let Some(result) = value.expr.as_deref()
        {
            self.returns
                .push((result, combine_predicates(&self.predicates)));
        }
        visit::visit_expr(self, expression);
    }

    fn visit_block(&mut self, block: &'source Block) {
        let depth = self.predicates.len();
        for (index, statement) in block.stmts.iter().enumerate() {
            let Stmt::Expr(expression, _) = statement else {
                self.visit_stmt(statement);
                continue;
            };
            if index + 1 == block.stmts.len() && block_tail(block).is_some() {
                let start = expression.span().start();
                self.tail_predicates.insert(
                    (start.line, start.column),
                    combine_predicates(&self.predicates[depth..]),
                );
            }
            self.visit_stmt(statement);
            if let Expr::If(branch) = peel(expression) {
                let then_returns = block_always_returns(&branch.then_branch);
                let else_returns = branch
                    .else_branch
                    .as_ref()
                    .is_some_and(|(_, alternative)| expression_always_returns(alternative));
                if then_returns != else_returns {
                    let predicate = source_text(&branch.cond);
                    self.predicates.push(if then_returns {
                        format!("!({predicate})")
                    } else {
                        predicate
                    });
                }
            }
        }
        self.predicates.truncate(depth);
    }

    fn visit_expr_if(&mut self, branch: &'source syn::ExprIf) {
        self.visit_expr(&branch.cond);
        let predicate = source_text(&branch.cond);
        self.predicates.push(predicate.clone());
        self.visit_block(&branch.then_branch);
        self.predicates.pop();
        if let Some((_, alternative)) = &branch.else_branch {
            self.predicates.push(format!("!({predicate})"));
            self.visit_expr(alternative);
            self.predicates.pop();
        }
    }

    fn visit_expr_match(&mut self, branch: &'source syn::ExprMatch) {
        self.visit_expr(&branch.expr);
        for arm in &branch.arms {
            self.predicates.push(match_predicate(&branch.expr, arm));
            self.visit_arm(arm);
            self.predicates.pop();
        }
    }

    fn visit_expr_closure(&mut self, closure: &'source syn::ExprClosure) {
        let mut nested = Self::from_expression(&closure.body);
        self.calls.append(&mut nested.calls);
    }

    fn visit_local(&mut self, local: &'source Local) {
        let pattern = match &local.pat {
            Pat::Type(typed) => &*typed.pat,
            pattern => pattern,
        };
        if let (Pat::Ident(identifier), Some(initializer)) = (pattern, &local.init) {
            self.locals
                .insert(identifier.ident.to_string(), &initializer.expr);
        }
        visit::visit_local(self, local);
    }

    fn visit_item(&mut self, _item: &'source syn::Item) {}
}

fn block_always_returns(block: &Block) -> bool {
    block.stmts.iter().any(|statement| {
        matches!(statement, Stmt::Expr(expression, _) if expression_always_returns(expression))
    })
}

fn expression_always_returns(expression: &Expr) -> bool {
    match peel(expression) {
        Expr::Return(_) => true,
        Expr::Block(block) => block_always_returns(&block.block),
        Expr::If(branch) => {
            block_always_returns(&branch.then_branch)
                && branch
                    .else_branch
                    .as_ref()
                    .is_some_and(|(_, alternative)| expression_always_returns(alternative))
        }
        Expr::Match(branch) => {
            !branch.arms.is_empty()
                && branch
                    .arms
                    .iter()
                    .all(|arm| expression_always_returns(&arm.body))
        }
        _ => false,
    }
}

fn combine_predicates(predicates: &[String]) -> String {
    predicates
        .iter()
        .fold(String::new(), |outer, inner| join_predicates(&outer, inner))
}

pub(crate) fn join_predicates(outer: &str, inner: &str) -> String {
    match (outer.is_empty(), inner.is_empty()) {
        (true, _) => inner.to_owned(),
        (_, true) => outer.to_owned(),
        _ => format!("({outer}) && ({inner})"),
    }
}

pub(crate) fn match_predicate(subject: &Expr, arm: &syn::Arm) -> String {
    let pattern = format!("{} matches {}", source_text(subject), source_text(&arm.pat));
    match &arm.guard {
        Some((_, guard)) => join_predicates(&pattern, &source_text(guard)),
        None => pattern,
    }
}

pub(crate) fn source_text(value: &impl Spanned) -> String {
    value
        .span()
        .source_text()
        .unwrap_or_default()
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ")
}

pub(crate) fn block_tail(block: &Block) -> Option<&Expr> {
    match block.stmts.last()? {
        Stmt::Expr(expression, None) if !matches!(expression, Expr::Return(_)) => Some(expression),
        _ => None,
    }
}

pub(crate) fn peel(mut expression: &Expr) -> &Expr {
    loop {
        expression = match expression {
            Expr::Reference(reference) => &reference.expr,
            Expr::Paren(parenthesized) => &parenthesized.expr,
            Expr::Group(group) => &group.expr,
            Expr::Try(attempt) => &attempt.expr,
            Expr::Unary(unary) if matches!(unary.op, UnOp::Deref(_)) => &unary.expr,
            _ => return expression,
        };
    }
}

pub(crate) fn expression_path(expression: &Expr) -> Option<String> {
    match peel(expression) {
        Expr::Path(expression) => expression
            .path
            .segments
            .last()
            .map(|segment| segment.ident.to_string()),
        _ => None,
    }
}

pub(crate) fn literal_string(expression: &Expr) -> Option<String> {
    match peel(expression) {
        Expr::Lit(value) => match &value.lit {
            syn::Lit::Str(value) => Some(value.value()),
            _ => None,
        },
        Expr::MethodCall(call)
            if matches!(
                call.method.to_string().as_str(),
                "to_string" | "to_owned" | "into"
            ) =>
        {
            literal_string(&call.receiver)
        }
        _ => None,
    }
}

pub(crate) fn type_name(value: &Type) -> Option<String> {
    match value {
        Type::Path(path) => path
            .path
            .segments
            .last()
            .map(|segment| segment.ident.to_string()),
        Type::Reference(reference) => type_name(&reference.elem),
        _ => None,
    }
}

pub(crate) fn associated_name(expression: &Expr) -> Option<(String, String)> {
    let Expr::Call(call) = peel(expression) else {
        return None;
    };
    let Expr::Path(function) = peel(&call.func) else {
        return None;
    };
    let mut segments = function.path.segments.iter().rev();
    let method = segments.next()?.ident.to_string();
    let owner = segments.next()?.ident.to_string();
    Some((owner, method))
}

pub(crate) fn associated_call(expression: &Expr, owner: &str, method: &str) -> bool {
    associated_name(expression).is_some_and(|(actual_owner, actual_method)| {
        actual_owner == owner && actual_method == method
    })
}

pub(crate) fn arguments(expression: &Expr) -> Option<&Punctuated<Expr, Token![,]>> {
    match peel(expression) {
        Expr::Call(call) => Some(&call.args),
        Expr::MethodCall(call) => Some(&call.args),
        _ => None,
    }
}

pub(crate) fn macro_arguments(
    value: &syn::Macro,
) -> Result<Punctuated<Expr, Token![,]>, syn::Error> {
    value.parse_body_with(Punctuated::<Expr, Token![,]>::parse_terminated)
}
