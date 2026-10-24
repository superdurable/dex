// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Bucket CORS configuration requests.

use std::sync::Arc;

use http::{HeaderMap, HeaderValue, StatusCode};
use s3mock::{CorsConfiguration, CorsRule, Store};

use super::bucket::status_response;
use super::request_body::read_small_body;
use crate::s3_error::S3Error;
use crate::server::{RequestBody, S3Response, run_store_operation, xml_response};
use crate::xml::{XmlElement, XmlWriter, parse_document};

const MAX_CORS_BODY_BYTES: usize = 64 * 1024;
const MAX_CORS_RULES: usize = 100;

pub(crate) async fn put_bucket_cors(
    store: Arc<Store>,
    bucket: String,
    headers: &HeaderMap,
    body: RequestBody,
) -> Result<S3Response, S3Error> {
    let body = read_small_body(body, headers, MAX_CORS_BODY_BYTES).await?;
    body.verify_integrity(headers, false)?;
    let document = parse_document(&body.bytes, "CORSConfiguration")?;
    if document
        .children
        .iter()
        .any(|child| child.name != "CORSRule")
    {
        return Err(S3Error::malformed_xml(
            "CORSConfiguration contains an unknown element",
        ));
    }
    let rules = document
        .children_named("CORSRule")
        .map(parse_rule)
        .collect::<Result<Vec<_>, _>>()?;
    if rules.is_empty() || rules.len() > MAX_CORS_RULES {
        return Err(S3Error::malformed_xml(
            "CORSConfiguration needs 1 to 100 CORSRule elements",
        ));
    }
    run_store_operation(&store, move |store| {
        store.put_cors_configuration(&bucket, Some(CorsConfiguration { rules }))
    })
    .await?;
    status_response(StatusCode::OK, &[])
}

pub(crate) async fn get_bucket_cors(
    store: Arc<Store>,
    bucket: String,
) -> Result<S3Response, S3Error> {
    let configuration =
        run_store_operation(&store, move |store| store.cors_configuration(&bucket)).await?;
    let mut document = XmlWriter::new("CORSConfiguration");
    for rule in &configuration.rules {
        document.open("CORSRule");
        document.optional_text_element("ID", rule.id.as_deref());
        for origin in &rule.allowed_origins {
            document.text_element("AllowedOrigin", origin);
        }
        for method in &rule.allowed_methods {
            document.text_element("AllowedMethod", method);
        }
        for header in &rule.allowed_headers {
            document.text_element("AllowedHeader", header);
        }
        for header in &rule.expose_headers {
            document.text_element("ExposeHeader", header);
        }
        if let Some(seconds) = rule.max_age_seconds {
            document.text_element("MaxAgeSeconds", &seconds.to_string());
        }
        document.close("CORSRule");
    }
    xml_response(document.finish("CORSConfiguration"))
}

pub(crate) async fn delete_bucket_cors(
    store: Arc<Store>,
    bucket: String,
) -> Result<S3Response, S3Error> {
    run_store_operation(&store, move |store| {
        store.put_cors_configuration(&bucket, None)
    })
    .await?;
    status_response(StatusCode::NO_CONTENT, &[])
}

fn parse_rule(rule: &XmlElement) -> Result<CorsRule, S3Error> {
    if rule.children.iter().any(|child| {
        !matches!(
            child.name.as_str(),
            "ID" | "AllowedOrigin"
                | "AllowedMethod"
                | "AllowedHeader"
                | "ExposeHeader"
                | "MaxAgeSeconds"
        )
    }) {
        return Err(S3Error::malformed_xml(
            "CORSRule contains an unknown element",
        ));
    }
    if rule.children_named("ID").count() > 1 || rule.children_named("MaxAgeSeconds").count() > 1 {
        return Err(S3Error::malformed_xml(
            "CORSRule repeats a singular element",
        ));
    }
    let allowed_origins = leaf_values(rule, "AllowedOrigin")?;
    let allowed_methods = leaf_values(rule, "AllowedMethod")?;
    let allowed_headers = leaf_values(rule, "AllowedHeader")?;
    let expose_headers = leaf_values(rule, "ExposeHeader")?;
    if allowed_origins.is_empty() || allowed_methods.is_empty() {
        return Err(S3Error::malformed_xml(
            "CORSRule needs an origin and method",
        ));
    }
    if allowed_origins
        .iter()
        .chain(&allowed_headers)
        .any(|value| value.matches('*').count() > 1)
    {
        return Err(S3Error::malformed_xml(
            "CORS wildcards may occur only once per value",
        ));
    }
    if allowed_origins
        .iter()
        .chain(&allowed_headers)
        .chain(&expose_headers)
        .any(|value| HeaderValue::from_str(value).is_err())
    {
        return Err(S3Error::malformed_xml(
            "CORS response header value is invalid",
        ));
    }
    if allowed_methods
        .iter()
        .any(|method| !matches!(method.as_str(), "GET" | "PUT" | "HEAD" | "POST" | "DELETE"))
    {
        return Err(S3Error::malformed_xml("AllowedMethod is not supported"));
    }
    let id = rule.child_text("ID")?.map(str::to_owned);
    if id.as_ref().is_some_and(|id| id.len() > 255) {
        return Err(S3Error::malformed_xml("CORSRule ID exceeds 255 characters"));
    }
    let max_age_seconds = rule
        .child_text("MaxAgeSeconds")?
        .map(|value| {
            value
                .parse()
                .map_err(|_| S3Error::malformed_xml("MaxAgeSeconds must be a nonnegative integer"))
        })
        .transpose()?;
    Ok(CorsRule {
        id,
        allowed_origins,
        allowed_methods,
        allowed_headers,
        expose_headers,
        max_age_seconds,
    })
}

fn leaf_values(rule: &XmlElement, name: &str) -> Result<Vec<String>, S3Error> {
    rule.children_named(name)
        .map(|child| {
            if !child.children.is_empty() || child.text.is_empty() {
                return Err(S3Error::malformed_xml(format!("{name} needs text")));
            }
            Ok(child.text.clone())
        })
        .collect()
}
