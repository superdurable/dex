// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Root-only credential management endpoints.

use std::sync::Arc;

use http::{Method, Request, StatusCode};
use http_body_util::{BodyExt, Limited};
use percent_encoding::percent_decode_str;
use s3mock::{CredentialRecord, CredentialStatus, Store};
use serde::Deserialize;
use serde_json::{Map, Value, json};

use crate::policy::CredentialPolicy;
use crate::server::{RequestBody, run_store_operation};

type AdminResult = Result<Value, (StatusCode, String)>;

pub(super) async fn dispatch_credentials(
    store: Arc<Store>,
    request: Request<RequestBody>,
    root_access_key_id: &str,
) -> AdminResult {
    let path = request.uri().path().to_owned();
    if path == "/_s3mock/credentials" {
        return match *request.method() {
            Method::POST => create_credential(store, request, root_access_key_id).await,
            Method::GET => list_credentials(store, request).await,
            _ => Err((
                StatusCode::NOT_FOUND,
                "unknown admin endpoint or method".into(),
            )),
        };
    }
    let access_key_id = path
        .strip_prefix("/_s3mock/credentials/")
        .and_then(|raw| percent_decode_str(raw).decode_utf8().ok())
        .ok_or((StatusCode::NOT_FOUND, "NoSuchCredential".into()))?
        .into_owned();
    if access_key_id.is_empty() || access_key_id.contains('/') {
        return Err((StatusCode::NOT_FOUND, "NoSuchCredential".into()));
    }
    match *request.method() {
        Method::GET => {
            let credential =
                run_store_operation(&store, move |store| store.credential(&access_key_id))
                    .await
                    .map_err(admin_store_error)?;
            Ok(public_record(&credential))
        }
        Method::PATCH => update_status(store, request, access_key_id).await,
        Method::DELETE => {
            run_store_operation(&store, move |store| store.delete_credential(&access_key_id))
                .await
                .map_err(admin_store_error)?;
            Ok(json!({"deleted": true}))
        }
        _ => Err((
            StatusCode::NOT_FOUND,
            "unknown admin endpoint or method".into(),
        )),
    }
}

async fn create_credential(
    store: Arc<Store>,
    request: Request<RequestBody>,
    root_access_key_id: &str,
) -> AdminResult {
    let body = read_json_body(request).await?;
    let fields: Value = serde_json::from_slice(&body)
        .map_err(|_| (StatusCode::BAD_REQUEST, "invalid credential JSON".into()))?;
    let fields = fields
        .as_object()
        .ok_or((StatusCode::BAD_REQUEST, "invalid credential".into()))?;
    if fields.keys().any(|field| {
        ![
            "access_key_id",
            "secret",
            "parent",
            "description",
            "policy",
            "status",
        ]
        .contains(&field.as_str())
    }) {
        return Err((StatusCode::BAD_REQUEST, "invalid credential field".into()));
    }
    let policy = fields
        .get("policy")
        .ok_or((StatusCode::BAD_REQUEST, "invalid policy".into()))?
        .clone();
    let status = match fields.get("status").and_then(Value::as_str) {
        Some("enabled") => CredentialStatus::Enabled,
        Some("disabled") => CredentialStatus::Disabled,
        _ => return Err((StatusCode::BAD_REQUEST, "invalid status".into())),
    };
    let credential = CredentialRecord {
        access_key_id: required_string(fields, "access_key_id")?,
        secret_access_key: required_string(fields, "secret")?,
        parent: required_string(fields, "parent")?,
        description: required_string(fields, "description")?,
        policy,
        status,
    };
    if credential.access_key_id.is_empty()
        || credential.access_key_id.contains('/')
        || credential.secret_access_key.is_empty()
        || credential.parent.is_empty()
    {
        return Err((
            StatusCode::BAD_REQUEST,
            "credential fields must be nonempty".into(),
        ));
    }
    if credential.access_key_id == root_access_key_id {
        return Err((StatusCode::CONFLICT, "CredentialAlreadyExists".into()));
    }
    CredentialPolicy::parse(credential.policy.clone())
        .map_err(|_| (StatusCode::BAD_REQUEST, "invalid policy".into()))?;
    let public = public_record(&credential);
    run_store_operation(&store, move |store| store.create_credential(&credential))
        .await
        .map_err(admin_store_error)?;
    Ok(public)
}

async fn list_credentials(store: Arc<Store>, request: Request<RequestBody>) -> AdminResult {
    let parent = request
        .uri()
        .query()
        .and_then(|query| {
            query.split('&').find_map(|pair| {
                pair.split_once('=')
                    .filter(|(name, _)| *name == "parent")
                    .map(|(_, value)| value)
            })
        })
        .ok_or((StatusCode::BAD_REQUEST, "parent is required".into()))?;
    let parent = percent_decode_str(parent)
        .decode_utf8()
        .map_err(|_| (StatusCode::BAD_REQUEST, "parent is invalid UTF-8".into()))?
        .into_owned();
    let credentials =
        run_store_operation(&store, move |store| store.credentials_by_parent(&parent))
            .await
            .map_err(admin_store_error)?;
    Ok(json!({
        "credentials": credentials.iter().map(public_record).collect::<Vec<_>>()
    }))
}

async fn update_status(
    store: Arc<Store>,
    request: Request<RequestBody>,
    access_key_id: String,
) -> AdminResult {
    #[derive(Deserialize)]
    #[serde(deny_unknown_fields)]
    struct StatusUpdate {
        status: CredentialStatus,
    }
    let body = read_json_body(request).await?;
    let update: StatusUpdate = serde_json::from_slice(&body)
        .map_err(|_| (StatusCode::BAD_REQUEST, "invalid status".into()))?;
    let credential = run_store_operation(&store, move |store| {
        store.update_credential_status(&access_key_id, update.status)
    })
    .await
    .map_err(admin_store_error)?;
    Ok(public_record(&credential))
}

async fn read_json_body(request: Request<RequestBody>) -> Result<Vec<u8>, (StatusCode, String)> {
    Limited::new(request.into_body(), 1024 * 1024)
        .collect()
        .await
        .map(|body| body.to_bytes().to_vec())
        .map_err(|_| (StatusCode::BAD_REQUEST, "invalid credential body".into()))
}

fn required_string(
    fields: &Map<String, Value>,
    name: &str,
) -> Result<String, (StatusCode, String)> {
    fields
        .get(name)
        .and_then(Value::as_str)
        .map(str::to_owned)
        .ok_or_else(|| (StatusCode::BAD_REQUEST, format!("invalid {name}")))
}

fn public_record(credential: &CredentialRecord) -> Value {
    json!({
        "access_key_id": credential.access_key_id,
        "parent": credential.parent,
        "description": credential.description,
        "policy": credential.policy,
        "status": credential.status,
    })
}

fn admin_store_error(error: crate::s3_error::S3Error) -> (StatusCode, String) {
    (error.status, error.code.into())
}
