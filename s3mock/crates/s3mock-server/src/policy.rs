// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The supported identity-policy subset and S3 operation permissions.

use http::{HeaderMap, StatusCode};
use serde::{Deserialize, Serialize};

use crate::routing::S3Operation;
use crate::s3_error::S3Error;

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct CredentialPolicy {
    #[serde(rename = "Version")]
    version: String,
    #[serde(rename = "Statement")]
    statements: OneOrMany<PolicyStatement>,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(untagged)]
enum OneOrMany<T> {
    One(T),
    Many(Vec<T>),
}

impl<T> OneOrMany<T> {
    fn values(&self) -> &[T] {
        match self {
            Self::One(value) => std::slice::from_ref(value),
            Self::Many(values) => values,
        }
    }
}

#[derive(Clone, Copy, Deserialize, Serialize)]
enum PolicyEffect {
    Allow,
    Deny,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct PolicyStatement {
    #[serde(rename = "Effect")]
    effect: PolicyEffect,
    #[serde(rename = "Action")]
    actions: OneOrMany<String>,
    #[serde(rename = "Resource")]
    resources: OneOrMany<String>,
}

impl CredentialPolicy {
    pub(crate) fn parse(value: serde_json::Value) -> Result<Self, String> {
        let policy: Self = serde_json::from_value(value).map_err(|error| error.to_string())?;
        if policy.version != "2012-10-17" {
            return Err("Version must be 2012-10-17".into());
        }
        if policy.statements.values().is_empty() {
            return Err("Statement must not be empty".into());
        }
        for statement in policy.statements.values() {
            if statement.actions.values().is_empty()
                || statement.actions.values().iter().any(|action| {
                    !action.starts_with("s3:")
                        || action.len() <= 3
                        || (action != "s3:*" && !action[3..].chars().all(char::is_alphanumeric))
                })
            {
                return Err("Action must contain exact s3 actions or s3:*".into());
            }
            if statement.resources.values().is_empty()
                || statement.resources.values().iter().any(|resource| {
                    let Some(path) = resource.strip_prefix("arn:aws:s3:::") else {
                        return true;
                    };
                    if path.is_empty() {
                        return true;
                    }
                    let bucket = path.split('/').next().unwrap_or_default();
                    bucket != "*" && (bucket.contains('*') || bucket.contains('?'))
                })
            {
                return Err("Resource must contain S3 bucket or object ARNs".into());
            }
        }
        Ok(policy)
    }

    pub(crate) fn authorize(&self, action: &str, resource: &str) -> Result<(), S3Error> {
        let mut has_allow = false;
        for statement in self.statements.values() {
            if !statement
                .actions
                .values()
                .iter()
                .any(|candidate| candidate == action || candidate == "s3:*")
                || !statement
                    .resources
                    .values()
                    .iter()
                    .any(|pattern| wildcard_match(pattern, resource))
            {
                continue;
            }
            match statement.effect {
                PolicyEffect::Deny => return Err(access_denied()),
                PolicyEffect::Allow => has_allow = true,
            }
        }
        if has_allow {
            Ok(())
        } else {
            Err(access_denied())
        }
    }
}

pub(crate) fn authorize_operation(
    policy: &CredentialPolicy,
    operation: &S3Operation,
    headers: &HeaderMap,
) -> Result<(), S3Error> {
    if matches!(operation, S3Operation::DeleteObjects { .. }) {
        return Ok(());
    }
    let (action, resource) = operation_permission(operation);
    policy.authorize(action, &resource)?;
    if matches!(operation, S3Operation::CreateBucket { .. })
        && headers
            .get("x-amz-bucket-object-lock-enabled")
            .is_some_and(|value| value.as_bytes().eq_ignore_ascii_case(b"true"))
    {
        policy.authorize("s3:PutBucketObjectLockConfiguration", &resource)?;
        policy.authorize("s3:PutBucketVersioning", &resource)?;
    }
    Ok(())
}

fn operation_permission(operation: &S3Operation) -> (&'static str, String) {
    use S3Operation::*;
    match operation {
        ListBuckets => ("s3:ListAllMyBuckets", "arn:aws:s3:::*".into()),
        CreateBucket { bucket } => ("s3:CreateBucket", bucket_arn(bucket)),
        HeadBucket { bucket } | ListObjectsV2 { bucket, .. } => {
            ("s3:ListBucket", bucket_arn(bucket))
        }
        DeleteBucket { bucket } => ("s3:DeleteBucket", bucket_arn(bucket)),
        GetBucketLocation { bucket } => ("s3:GetBucketLocation", bucket_arn(bucket)),
        GetBucketVersioning { bucket } => ("s3:GetBucketVersioning", bucket_arn(bucket)),
        PutBucketVersioning { bucket } => ("s3:PutBucketVersioning", bucket_arn(bucket)),
        GetBucketLifecycleConfiguration { bucket } => {
            ("s3:GetLifecycleConfiguration", bucket_arn(bucket))
        }
        PutBucketLifecycleConfiguration { bucket } | DeleteBucketLifecycle { bucket } => {
            ("s3:PutLifecycleConfiguration", bucket_arn(bucket))
        }
        GetObjectLockConfiguration { bucket } => {
            ("s3:GetBucketObjectLockConfiguration", bucket_arn(bucket))
        }
        PutObjectLockConfiguration { bucket } => {
            ("s3:PutBucketObjectLockConfiguration", bucket_arn(bucket))
        }
        ListMultipartUploads { bucket, .. } => {
            ("s3:ListBucketMultipartUploads", bucket_arn(bucket))
        }
        ListObjectVersions { bucket, .. } => ("s3:ListBucketVersions", bucket_arn(bucket)),
        DeleteObjects { bucket } => ("s3:DeleteObject", bucket_arn(bucket)),
        CreateMultipartUpload { bucket, key }
        | UploadPart { bucket, key, .. }
        | CompleteMultipartUpload { bucket, key, .. }
        | PutObject { bucket, key } => ("s3:PutObject", object_arn(bucket, key)),
        AbortMultipartUpload { bucket, key, .. } => {
            ("s3:AbortMultipartUpload", object_arn(bucket, key))
        }
        ListParts { bucket, key, .. } => ("s3:ListMultipartUploadParts", object_arn(bucket, key)),
        GetObject { bucket, key, query } | HeadObject { bucket, key, query } => (
            if query.get("versionId").is_some() {
                "s3:GetObjectVersion"
            } else {
                "s3:GetObject"
            },
            object_arn(bucket, key),
        ),
        DeleteObject { bucket, key, query } => (
            if query.get("versionId").is_some() {
                "s3:DeleteObjectVersion"
            } else {
                "s3:DeleteObject"
            },
            object_arn(bucket, key),
        ),
    }
}

pub(crate) fn object_arn(bucket: &str, key: &str) -> String {
    format!("arn:aws:s3:::{bucket}/{key}")
}

fn bucket_arn(bucket: &str) -> String {
    format!("arn:aws:s3:::{bucket}")
}

fn wildcard_match(pattern: &str, value: &str) -> bool {
    let pattern = pattern.as_bytes();
    let value = value.as_bytes();
    let mut previous = vec![false; value.len() + 1];
    previous[0] = true;
    for symbol in pattern {
        let mut current = vec![false; value.len() + 1];
        if *symbol == b'*' {
            current[0] = previous[0];
        }
        for index in 1..=value.len() {
            current[index] = match symbol {
                b'*' => previous[index] || current[index - 1],
                b'?' => previous[index - 1],
                _ => previous[index - 1] && *symbol == value[index - 1],
            };
        }
        previous = current;
    }
    previous[value.len()]
}

fn access_denied() -> S3Error {
    S3Error::new(
        StatusCode::FORBIDDEN,
        "AccessDenied",
        "the credential policy does not allow this operation",
    )
}
