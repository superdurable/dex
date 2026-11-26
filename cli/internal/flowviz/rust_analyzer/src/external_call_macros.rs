// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use proc_macro2::{Delimiter, Spacing, TokenStream, TokenTree};
use syn::punctuated::Punctuated;
use syn::{Expr, Token};

/// Parses macro arguments as expressions, reading json! objects and tracing fields; None means unreadable.
pub(crate) fn macro_argument_expressions(tokens: TokenStream) -> Option<Vec<Expr>> {
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
