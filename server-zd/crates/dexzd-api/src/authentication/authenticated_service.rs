// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Bearer credential checks in front of one public gRPC service.

use std::future::Future;
use std::pin::Pin;
use std::sync::Arc;
use std::task::{Context, Poll};

use dexzd_protocol::dex::ErrorSubStatus;
use tonic::Code;
use tonic::codegen::{Service, http};
use tonic::server::NamedService;

use super::PublicApiAuthenticator;
use crate::FlowOperationError;

/// Largest `authorization` header value accepted, as in the Go Server.
pub(crate) const MAX_CREDENTIAL_BYTES: usize = 8 << 10;
const BEARER_PREFIX: &str = "bearer ";
const MISSING_CREDENTIAL_DETAIL: &str = "a bearer credential is required";
const INVALID_CREDENTIAL_DETAIL: &str = "the credential is not valid";
const NOT_ADMIN_CREDENTIAL_DETAIL: &str =
    "admin RPCs require a JWT whose audience is listed in auth.adminAudiences";

/// Requires a valid bearer credential when each call starts, so open streams outlive credential expiry.
#[derive(Clone)]
pub struct AuthenticatedService<S> {
    inner: S,
    authorization: CallAuthorization,
}

/// Which credentials a wrapped service accepts.
#[derive(Clone)]
enum CallAuthorization {
    /// No authenticator is configured, so every call passes unchecked.
    Open,
    /// A credential that the public API authenticator accepts.
    PublicApi(Arc<PublicApiAuthenticator>),
    /// A JWT naming an admin audience; `public` tells a refused caller from an unknown one.
    Admin {
        admin: Arc<PublicApiAuthenticator>,
        public: Arc<PublicApiAuthenticator>,
    },
}

impl<S> AuthenticatedService<S> {
    /// Wraps `inner`; with `authenticator` set to `None`, every call passes through unchecked.
    pub fn new(inner: S, authenticator: Option<Arc<PublicApiAuthenticator>>) -> Self {
        Self {
            inner,
            authorization: authenticator
                .map_or(CallAuthorization::Open, CallAuthorization::PublicApi),
        }
    }

    /// Wraps an admin service that needs a JWT from a configured issuer naming one of `admin_audiences`.
    ///
    /// With `authenticator` set to `None`, as on a development server, every call passes unchecked.
    pub fn for_admin_service(
        inner: S,
        authenticator: Option<Arc<PublicApiAuthenticator>>,
        admin_audiences: &[String],
    ) -> Self {
        let authorization = match authenticator {
            None => CallAuthorization::Open,
            Some(public) => CallAuthorization::Admin {
                admin: Arc::new(public.for_admin_audiences(admin_audiences)),
                public,
            },
        };
        Self {
            inner,
            authorization,
        }
    }
}

impl<S: NamedService> NamedService for AuthenticatedService<S> {
    const NAME: &'static str = S::NAME;
}

impl<S, RequestBody, ResponseBody> Service<http::Request<RequestBody>> for AuthenticatedService<S>
where
    S: Service<http::Request<RequestBody>, Response = http::Response<ResponseBody>>
        + NamedService
        + Clone
        + Send
        + 'static,
    S::Future: Send + 'static,
    RequestBody: Send + 'static,
    ResponseBody: Default + Send + 'static,
{
    type Response = S::Response;
    type Error = S::Error;
    type Future = Pin<Box<dyn Future<Output = Result<Self::Response, Self::Error>> + Send>>;

    fn poll_ready(&mut self, context: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        self.inner.poll_ready(context)
    }

    fn call(&mut self, request: http::Request<RequestBody>) -> Self::Future {
        if matches!(self.authorization, CallAuthorization::Open) {
            return Box::pin(self.inner.call(request));
        }
        let authorization = self.authorization.clone();
        let credential = bearer_credential(request.headers()).map(str::to_owned);
        // The ready inner service handles this call; its clone serves the next one.
        let ready_inner = self.inner.clone();
        let mut inner = std::mem::replace(&mut self.inner, ready_inner);
        Box::pin(async move {
            let Some(credential) = credential else {
                tracing::debug!(
                    service = S::NAME,
                    "rejected a public API call without a bearer credential"
                );
                return Ok(refused(Code::Unauthenticated, MISSING_CREDENTIAL_DETAIL));
            };
            if let Err((code, detail)) = authorization.authorize(S::NAME, &credential).await {
                return Ok(refused(code, detail));
            }
            inner.call(request).await
        })
    }
}

impl CallAuthorization {
    /// Returns `Ok` when `credential` authorizes the call, or the refused status code and detail.
    async fn authorize(
        &self,
        service_name: &'static str,
        credential: &str,
    ) -> Result<(), (Code, &'static str)> {
        match self {
            Self::Open => Ok(()),
            Self::PublicApi(authenticator) => {
                authenticator
                    .authenticate(credential)
                    .await
                    .map_err(|rejection| {
                        tracing::debug!(
                            service = service_name,
                            reason = %rejection,
                            "rejected a public API credential"
                        );
                        (Code::Unauthenticated, INVALID_CREDENTIAL_DETAIL)
                    })
            }
            Self::Admin { admin, public } => {
                let Err(rejection) = admin.authenticate(credential).await else {
                    return Ok(());
                };
                tracing::debug!(
                    service = service_name,
                    reason = %rejection,
                    "rejected an admin credential"
                );
                if public.authenticate(credential).await.is_ok() {
                    Err((Code::PermissionDenied, NOT_ADMIN_CREDENTIAL_DETAIL))
                } else {
                    Err((Code::Unauthenticated, INVALID_CREDENTIAL_DETAIL))
                }
            }
        }
    }
}

/// Returns the trimmed bearer credential of exactly one `authorization` value of at most 8 KiB.
fn bearer_credential(headers: &http::HeaderMap) -> Option<&str> {
    let mut values = headers.get_all(http::header::AUTHORIZATION).iter();
    let (Some(value), None) = (values.next(), values.next()) else {
        return None;
    };
    let value = value.to_str().ok()?;
    if value.len() <= BEARER_PREFIX.len() || value.len() > MAX_CREDENTIAL_BYTES {
        return None;
    }
    let (scheme, credential) = value.split_at_checked(BEARER_PREFIX.len())?;
    if !scheme.eq_ignore_ascii_case(BEARER_PREFIX) {
        return None;
    }
    Some(credential.trim()).filter(|credential| !credential.is_empty())
}

fn refused<ResponseBody: Default>(code: Code, detail: &str) -> http::Response<ResponseBody> {
    FlowOperationError::rejected(code, ErrorSubStatus::Uncategorized, detail)
        .into_status()
        .into_http()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn headers(values: &[&str]) -> http::HeaderMap {
        let mut headers = http::HeaderMap::new();
        for value in values {
            headers.append(
                http::header::AUTHORIZATION,
                http::HeaderValue::from_str(value).expect("the test header value is valid"),
            );
        }
        headers
    }

    #[test]
    fn bearer_credentials_follow_the_go_server_rules() {
        assert_eq!(
            bearer_credential(&headers(&["Bearer token-1"])),
            Some("token-1")
        );
        assert_eq!(
            bearer_credential(&headers(&["bEaReR  token-2 "])),
            Some("token-2")
        );
        assert_eq!(bearer_credential(&headers(&[])), None);
        assert_eq!(bearer_credential(&headers(&["Bearer "])), None);
        assert_eq!(bearer_credential(&headers(&["Bearer    "])), None);
        assert_eq!(bearer_credential(&headers(&["Basic dXNlcjpwYXNz"])), None);
        assert_eq!(bearer_credential(&headers(&["Bearer a", "Bearer b"])), None);
        let longest = format!("Bearer {}", "a".repeat(MAX_CREDENTIAL_BYTES - 7));
        assert_eq!(longest.len(), MAX_CREDENTIAL_BYTES);
        assert!(bearer_credential(&headers(&[&longest])).is_some());
        let too_long = format!("{longest}a");
        assert_eq!(bearer_credential(&headers(&[&too_long])), None);
    }
}
