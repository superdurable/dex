// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Evaluates bucket CORS rules for preflight and actual requests.

use std::sync::Arc;

use http::{HeaderMap, HeaderValue, Method, Response, StatusCode, Uri};
use percent_encoding::percent_decode_str;
use s3mock::{CorsConfiguration, CorsRule, Store};

use crate::s3_error::S3Error;
use crate::server::{S3Response, empty_body, run_store_operation};

const VARY: &str = "Origin, Access-Control-Request-Headers, Access-Control-Request-Method";

pub(crate) async fn preflight(
    store: &Arc<Store>,
    uri: &Uri,
    request_headers: &HeaderMap,
) -> Result<S3Response, S3Error> {
    let requested_method = request_headers
        .get("access-control-request-method")
        .and_then(|value| value.to_str().ok())
        .ok_or_else(cors_forbidden)?;
    let requested_headers = request_headers
        .get("access-control-request-headers")
        .and_then(|value| value.to_str().ok())
        .map(|value| {
            value
                .split(',')
                .map(str::trim)
                .filter(|header| !header.is_empty())
                .collect::<Vec<_>>()
        })
        .unwrap_or_default();
    let headers = evaluate(
        store,
        uri,
        request_headers,
        requested_method,
        &requested_headers,
        true,
    )
    .await?;
    Response::builder()
        .status(StatusCode::OK)
        .body(empty_body())
        .map(|mut response| {
            response.headers_mut().extend(headers);
            response
        })
        .map_err(|error| S3Error::internal(format!("build CORS response: {error}")))
}

pub(crate) async fn actual_headers(
    store: &Arc<Store>,
    method: &Method,
    uri: &Uri,
    request_headers: &HeaderMap,
) -> Result<Option<HeaderMap>, S3Error> {
    if !request_headers.contains_key("origin") {
        return Ok(None);
    }
    match evaluate(store, uri, request_headers, method.as_str(), &[], false).await {
        Ok(headers) => Ok(Some(headers)),
        Err(error) if error.code == "AccessForbidden" => Ok(None),
        Err(error) => Err(error),
    }
}

async fn evaluate(
    store: &Arc<Store>,
    uri: &Uri,
    request_headers: &HeaderMap,
    method: &str,
    requested_headers: &[&str],
    is_preflight: bool,
) -> Result<HeaderMap, S3Error> {
    let origin = request_headers
        .get("origin")
        .and_then(|value| value.to_str().ok())
        .ok_or_else(cors_forbidden)?;
    let bucket = uri
        .path()
        .trim_start_matches('/')
        .split('/')
        .next()
        .and_then(|value| percent_decode_str(value).decode_utf8().ok())
        .filter(|value| !value.is_empty())
        .ok_or_else(cors_forbidden)?
        .into_owned();
    let configuration = run_store_operation(store, move |store| store.cors_configuration(&bucket))
        .await
        .map_err(|error| {
            if error.code == "NoSuchCORSConfiguration" {
                cors_forbidden()
            } else {
                error
            }
        })?;
    let rule =
        match_rule(&configuration, origin, method, requested_headers).ok_or_else(cors_forbidden)?;
    response_headers(rule, origin, requested_headers, is_preflight)
}

fn match_rule<'rule>(
    configuration: &'rule CorsConfiguration,
    origin: &str,
    method: &str,
    requested_headers: &[&str],
) -> Option<&'rule CorsRule> {
    configuration.rules.iter().find(|rule| {
        rule.allowed_origins
            .iter()
            .any(|pattern| wildcard_match(pattern, origin, false))
            && rule.allowed_methods.iter().any(|allowed| allowed == method)
            && requested_headers.iter().all(|requested| {
                rule.allowed_headers
                    .iter()
                    .any(|pattern| wildcard_match(pattern, requested, true))
            })
    })
}

fn wildcard_match(pattern: &str, value: &str, is_case_insensitive: bool) -> bool {
    let (pattern, value) = if is_case_insensitive {
        (pattern.to_ascii_lowercase(), value.to_ascii_lowercase())
    } else {
        (pattern.to_owned(), value.to_owned())
    };
    match pattern.split_once('*') {
        Some((prefix, suffix)) => {
            value.starts_with(prefix)
                && value.ends_with(suffix)
                && value.len() >= prefix.len() + suffix.len()
        }
        None => pattern == value,
    }
}

fn response_headers(
    rule: &CorsRule,
    origin: &str,
    requested_headers: &[&str],
    is_preflight: bool,
) -> Result<HeaderMap, S3Error> {
    let mut headers = HeaderMap::new();
    add_header(&mut headers, "access-control-allow-origin", origin)?;
    add_header(&mut headers, "access-control-allow-credentials", "true")?;
    add_header(
        &mut headers,
        "access-control-allow-methods",
        &rule.allowed_methods.join(", "),
    )?;
    add_header(&mut headers, "vary", VARY)?;
    if is_preflight && !requested_headers.is_empty() {
        add_header(
            &mut headers,
            "access-control-allow-headers",
            &requested_headers.join(", "),
        )?;
    }
    if !rule.expose_headers.is_empty() {
        add_header(
            &mut headers,
            "access-control-expose-headers",
            &rule.expose_headers.join(", "),
        )?;
    }
    if let Some(seconds) = rule.max_age_seconds {
        add_header(&mut headers, "access-control-max-age", &seconds.to_string())?;
    }
    Ok(headers)
}

fn add_header(headers: &mut HeaderMap, name: &'static str, value: &str) -> Result<(), S3Error> {
    let value = HeaderValue::from_str(value)
        .map_err(|_| S3Error::internal(format!("stored CORS {name} value is invalid")))?;
    headers.insert(name, value);
    Ok(())
}

fn cors_forbidden() -> S3Error {
    S3Error::new(
        StatusCode::FORBIDDEN,
        "AccessForbidden",
        "CORSResponse: This CORS request is not allowed.",
    )
}
