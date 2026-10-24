// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! SigV4 header authentication with disk-backed verification before store dispatch.

mod payload;

use std::collections::BTreeMap;
use std::io;
use std::path::Path;
use std::sync::Arc;

use futures_util::{StreamExt, TryStreamExt};
use hmac::{Hmac, Mac};
use http::{Request, StatusCode};
use http_body::Body;
use http_body_util::{BodyExt, StreamBody};
use hyper::body::Frame;
use percent_encoding::percent_decode_str;
use s3mock::{CredentialStatus, Store, StoreError};
use sha2::{Digest, Sha256};
use tokio::io::{AsyncSeekExt, AsyncWriteExt};
use tokio_util::io::ReaderStream;

use crate::config::{ServerConfig, SignatureVerification};
use crate::policy::CredentialPolicy;
use crate::s3_error::S3Error;
use crate::server::RequestBody;

pub(crate) struct SignatureContext {
    key: Vec<u8>,
    timestamp: String,
    scope: String,
    signature: String,
    payload_hash: String,
    trailer_names: Vec<String>,
}

pub(crate) enum Identity {
    Root,
    Scoped(CredentialPolicy),
}

impl Identity {
    pub(crate) fn policy(&self) -> Option<&CredentialPolicy> {
        match self {
            Self::Root => None,
            Self::Scoped(policy) => Some(policy),
        }
    }

    pub(crate) fn is_root(&self) -> bool {
        matches!(self, Self::Root)
    }
}

pub(crate) async fn verify_request(
    request: Request<RequestBody>,
    config: &ServerConfig,
    store: &Arc<Store>,
    spool_directory: &Path,
) -> Result<(Request<RequestBody>, Identity), S3Error> {
    if config.signature_verification == SignatureVerification::Off {
        return Ok((request, Identity::Root));
    }
    if request
        .uri()
        .query()
        .is_some_and(|query| query.split('&').any(|pair| pair.starts_with("X-Amz-")))
    {
        return Err(S3Error::not_implemented(
            "presigned URLs in strict signature mode",
        ));
    }
    let access_key_id = access_key_id(&request)?;
    let (secret_access_key, identity) = if access_key_id == config.access_key_id {
        (config.secret_access_key.clone(), Identity::Root)
    } else {
        let access_key_id = access_key_id.to_owned();
        let store = Arc::clone(store);
        let credential = tokio::task::spawn_blocking(move || store.credential(&access_key_id))
            .await
            .map_err(|error| S3Error::internal(format!("credential task failed: {error}")))?
            .map_err(|error| match error {
                StoreError::NoSuchCredential => access_denied(),
                other => S3Error::from(other),
            })?;
        if credential.status == CredentialStatus::Disabled {
            return Err(S3Error::new(
                StatusCode::FORBIDDEN,
                "InvalidAccessKeyId",
                "the access key ID is disabled",
            ));
        }
        let policy = CredentialPolicy::parse(credential.policy)
            .map_err(|error| S3Error::internal(format!("stored credential policy: {error}")))?;
        (credential.secret_access_key, Identity::Scoped(policy))
    };
    let context = verify_headers(&request, &access_key_id, &secret_access_key)?;
    if request.body().is_end_stream() {
        if !matches!(context.payload_hash.as_str(), "UNSIGNED-PAYLOAD")
            && context.payload_hash != hash(b"")
        {
            return Err(mismatch());
        }
        return Ok((request, identity));
    }
    let spool_directory = spool_directory.to_owned();
    let (parts, mut body) = request.into_parts();
    let file = tokio::task::spawn_blocking(move || tempfile::tempfile_in(spool_directory))
        .await
        .map_err(|error| S3Error::internal(format!("create signature spool task: {error}")))?
        .map_err(|error| S3Error::internal(format!("create signature spool file: {error}")))?;
    let mut file = tokio::fs::File::from_std(file);
    let mut trailers = None;
    let mut bytes_received = 0_u64;
    while let Some(frame) = body.frame().await {
        let frame = frame
            .map_err(|error| S3Error::incomplete_body(format!("read signed body: {error}")))?;
        match frame.into_data() {
            Ok(data) => {
                bytes_received += data.len() as u64;
                if bytes_received > 6 * 1024 * 1024 * 1024 {
                    return Err(S3Error::new(
                        StatusCode::BAD_REQUEST,
                        "EntityTooLarge",
                        "signed body exceeds the 6 GiB encoded body limit",
                    ));
                }
                file.write_all(&data)
                    .await
                    .map_err(|error| S3Error::internal(format!("spool signed body: {error}")))?;
            }
            Err(frame) => {
                if let Ok(headers) = frame.into_trailers() {
                    trailers = Some(headers);
                }
            }
        }
    }
    file.rewind()
        .await
        .map_err(|error| S3Error::internal(format!("rewind signed body: {error}")))?;
    let file = file.into_std().await;
    let file = tokio::task::spawn_blocking(move || payload::verify(file, context))
        .await
        .map_err(|error| S3Error::internal(format!("verify signed body task: {error}")))??;
    let stream = ReaderStream::new(tokio::fs::File::from_std(file)).map_ok(Frame::data);
    let trailer_stream = futures_util::stream::iter(
        trailers
            .into_iter()
            .map(|headers| Ok::<_, io::Error>(Frame::trailers(headers))),
    );
    Ok((
        Request::from_parts(
            parts,
            BodyExt::boxed(StreamBody::new(stream.chain(trailer_stream))),
        ),
        identity,
    ))
}

fn access_key_id(request: &Request<RequestBody>) -> Result<String, S3Error> {
    let authorization = request
        .headers()
        .get("authorization")
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.strip_prefix("AWS4-HMAC-SHA256 "))
        .ok_or_else(access_denied)?;
    let credential = authorization
        .split(',')
        .find_map(|field| field.trim().strip_prefix("Credential="))
        .ok_or_else(access_denied)?;
    Ok(credential
        .split('/')
        .next()
        .ok_or_else(access_denied)?
        .to_owned())
}

fn verify_headers(
    request: &Request<RequestBody>,
    access_key_id: &str,
    secret_access_key: &str,
) -> Result<SignatureContext, S3Error> {
    let header = |name: &str| {
        request
            .headers()
            .get(name)
            .and_then(|value| value.to_str().ok())
            .ok_or_else(access_denied)
    };
    let authorization = header("authorization")?
        .strip_prefix("AWS4-HMAC-SHA256 ")
        .ok_or_else(access_denied)?;
    let mut fields = BTreeMap::new();
    for field in authorization.split(',') {
        let (name, value) = field.trim().split_once('=').ok_or_else(access_denied)?;
        if fields.insert(name, value).is_some() {
            return Err(access_denied());
        }
    }
    if fields.len() != 3 {
        return Err(access_denied());
    }
    let credential = fields.get("Credential").ok_or_else(access_denied)?;
    let components: Vec<_> = credential.split('/').collect();
    if components.len() != 5
        || components[0] != access_key_id
        || components[3] != "s3"
        || components[4] != "aws4_request"
    {
        return Err(access_denied());
    }
    if components[2] != "us-east-1" {
        return Err(S3Error::new(
            StatusCode::BAD_REQUEST,
            "AuthorizationHeaderMalformed",
            "the credential region must be us-east-1",
        ));
    }
    let timestamp = header("x-amz-date")?;
    if timestamp.len() != 16
        || timestamp.get(..8) != Some(components[1])
        || timestamp.as_bytes()[8] != b'T'
        || !timestamp.ends_with('Z')
        || !timestamp.as_bytes()[..8]
            .iter()
            .chain(&timestamp.as_bytes()[9..15])
            .all(u8::is_ascii_digit)
    {
        return Err(access_denied());
    }
    let signed_headers = *fields.get("SignedHeaders").ok_or_else(access_denied)?;
    let names: Vec<_> = signed_headers.split(';').collect();
    if !names.contains(&"host")
        || !names.contains(&"x-amz-date")
        || names.windows(2).any(|pair| pair[0] >= pair[1])
    {
        return Err(access_denied());
    }
    if request.headers().keys().any(|name| {
        name.as_str().starts_with("x-amz-")
            && name.as_str() != "x-amz-content-sha256"
            && !names.contains(&name.as_str())
    }) {
        return Err(access_denied());
    }
    let mut canonical_headers = String::new();
    for name in names {
        if name.is_empty() || name != name.to_ascii_lowercase() {
            return Err(access_denied());
        }
        let values: Vec<_> = request
            .headers()
            .get_all(name)
            .iter()
            .map(|value| {
                value
                    .to_str()
                    .map(|value| value.split_whitespace().collect::<Vec<_>>().join(" "))
                    .map_err(|_| access_denied())
            })
            .collect::<Result<_, _>>()?;
        if values.is_empty() {
            return Err(access_denied());
        }
        canonical_headers.push_str(&format!("{name}:{}\n", values.join(",")));
    }
    let payload_hash = header("x-amz-content-sha256")?;
    let mut query = Vec::new();
    for pair in request
        .uri()
        .query()
        .unwrap_or_default()
        .split('&')
        .filter(|pair| !pair.is_empty())
    {
        let (name, value) = pair.split_once('=').unwrap_or((pair, ""));
        query.push((
            encode(&percent_decode_str(name).collect::<Vec<_>>(), false),
            encode(&percent_decode_str(value).collect::<Vec<_>>(), false),
        ));
    }
    query.sort();
    let canonical_query = query
        .iter()
        .map(|(name, value)| format!("{name}={value}"))
        .collect::<Vec<_>>()
        .join("&");
    let path = encode(
        &percent_decode_str(request.uri().path()).collect::<Vec<_>>(),
        true,
    );
    let canonical = format!(
        "{}\n{path}\n{canonical_query}\n{canonical_headers}\n{signed_headers}\n{payload_hash}",
        request.method()
    );
    let scope = components[1..].join("/");
    let key = hmac(
        format!("AWS4{secret_access_key}").as_bytes(),
        components[1].as_bytes(),
    );
    let key = hmac(&key, components[2].as_bytes());
    let key = hmac(&key, b"s3");
    let key = hmac(&key, b"aws4_request");
    let signature = fields
        .get("Signature")
        .ok_or_else(access_denied)?
        .to_string();
    verify_hmac(
        &key,
        &format!(
            "AWS4-HMAC-SHA256\n{timestamp}\n{scope}\n{}",
            hash(canonical.as_bytes())
        ),
        &signature,
    )?;
    let trailer_names = request
        .headers()
        .get("x-amz-trailer")
        .map(|value| value.to_str().map_err(|_| access_denied()))
        .transpose()?
        .unwrap_or_default()
        .split(',')
        .filter(|name| !name.is_empty())
        .map(|name| name.trim().to_ascii_lowercase())
        .collect();
    Ok(SignatureContext {
        key,
        timestamp: timestamp.into(),
        scope,
        signature,
        payload_hash: payload_hash.into(),
        trailer_names,
    })
}

fn encode(bytes: &[u8], preserve_slash: bool) -> String {
    let mut encoded = String::new();
    for byte in bytes {
        if byte.is_ascii_alphanumeric()
            || b"-._~".contains(byte)
            || (preserve_slash && *byte == b'/')
        {
            encoded.push(char::from(*byte));
        } else {
            encoded.push_str(&format!("%{byte:02X}"));
        }
    }
    encoded
}

fn hash(bytes: &[u8]) -> String {
    hex::encode(Sha256::digest(bytes))
}

fn hmac(key: &[u8], bytes: &[u8]) -> Vec<u8> {
    let mut mac = Hmac::<Sha256>::new_from_slice(key).expect("HMAC accepts keys of any length");
    mac.update(bytes);
    mac.finalize().into_bytes().to_vec()
}

fn verify_hmac(key: &[u8], message: &str, expected: &str) -> Result<(), S3Error> {
    let signature = hex::decode(expected).map_err(|_| mismatch())?;
    let mut mac = Hmac::<Sha256>::new_from_slice(key).expect("HMAC accepts keys of any length");
    mac.update(message.as_bytes());
    mac.verify_slice(&signature).map_err(|_| mismatch())
}

fn mismatch() -> S3Error {
    S3Error::new(
        StatusCode::FORBIDDEN,
        "SignatureDoesNotMatch",
        "the request signature does not match",
    )
}

fn access_denied() -> S3Error {
    S3Error::new(
        StatusCode::FORBIDDEN,
        "AccessDenied",
        "valid SigV4 header credentials are required",
    )
}
