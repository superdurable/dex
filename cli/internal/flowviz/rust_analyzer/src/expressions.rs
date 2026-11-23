// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;

use syn::punctuated::Punctuated;
use syn::visit::{self, Visit};
use syn::{Block, Expr, Local, Pat, Stmt, Token, Type};

pub(crate) struct ExpressionCalls<'source> {
    pub(crate) calls: Vec<&'source Expr>,
    pub(crate) locals: BTreeMap<String, &'source Expr>,
    tail: Option<&'source Expr>,
    returns: Vec<&'source Expr>,
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
        }
    }

    pub(crate) fn return_expressions(&self) -> Vec<&'source Expr> {
        self.returns.iter().copied().chain(self.tail).collect()
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
        if let Expr::Return(value) = expression {
            self.returns.extend(value.expr.as_deref());
        }
        visit::visit_expr(self, expression);
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
