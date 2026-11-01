// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Admin credential checks, the verified caller they pass to admin handlers, and denial logs.

use std::sync::{Arc, Mutex, PoisonError};
use std::time::{Duration, Instant};

use tonic::Code;

use super::authenticated_service::INVALID_CREDENTIAL_DETAIL;
use super::{CredentialIdentity, CredentialRejection, PublicApiAuthenticator};

const NOT_ADMIN_CREDENTIAL_DETAIL: &str =
    "admin RPCs require a JWT whose audience is listed in its jwt authenticator's adminAudiences";
/// Shortest gap between two denied admin call logs; the next one reports the skipped count.
const DENIED_ADMIN_CALL_LOG_INTERVAL: Duration = Duration::from_secs(10);

/// The verified caller of an authorized admin call, passed to the handler in request extensions.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(super) struct AdminCaller {
    /// Verified `iss` claim of the admin JWT.
    issuer: String,
    /// `sub` claim of the admin JWT when it is a string.
    subject: Option<String>,
}

/// Log fields of an admin call's caller; empty when authentication is off.
pub(crate) struct AdminCallerLogFields {
    caller: Option<AdminCaller>,
}

impl AdminCallerLogFields {
    /// Reads the caller that admin authentication recorded on `request`.
    pub(crate) fn of_request<T>(request: &tonic::Request<T>) -> Self {
        Self {
            caller: request.extensions().get::<AdminCaller>().cloned(),
        }
    }

    pub(crate) fn is_authenticated(&self) -> bool {
        self.caller.is_some()
    }

    pub(crate) fn issuer(&self) -> &str {
        self.caller
            .as_ref()
            .map_or("", |caller| caller.issuer.as_str())
    }

    pub(crate) fn subject(&self) -> &str {
        self.caller
            .as_ref()
            .and_then(|caller| caller.subject.as_deref())
            .unwrap_or("")
    }
}

/// Accepts only JWTs naming an admin audience of their issuer, and logs denied calls.
pub(super) struct AdminAuthorization {
    admin: PublicApiAuthenticator,
    /// Tells a valid public API credential (PERMISSION_DENIED) from an unknown one (UNAUTHENTICATED).
    public: Arc<PublicApiAuthenticator>,
    denied_call_log: DeniedAdminCallLog,
}

impl AdminAuthorization {
    pub(super) fn new(public: Arc<PublicApiAuthenticator>) -> Self {
        Self {
            admin: public.admin_authenticator(),
            public,
            denied_call_log: DeniedAdminCallLog::default(),
        }
    }

    /// Returns the verified caller, or the refused status code and detail.
    pub(super) async fn authorize(
        &self,
        path: &str,
        credential: &str,
    ) -> Result<AdminCaller, (Code, &'static str)> {
        let rejection = match self.admin.authenticate(credential).await {
            Ok(CredentialIdentity::Jwt { issuer, subject }) => {
                return Ok(AdminCaller { issuer, subject });
            }
            Ok(CredentialIdentity::StaticToken) => {
                unreachable!("the admin authenticator holds no static tokens")
            }
            Err(rejection) => rejection,
        };
        // The logged admin rejection already explains the denial, so the public one is dropped.
        let public_identity = self.public.authenticate(credential).await.ok();
        let (code, detail) = if public_identity.is_some() {
            (Code::PermissionDenied, NOT_ADMIN_CREDENTIAL_DETAIL)
        } else {
            (Code::Unauthenticated, INVALID_CREDENTIAL_DETAIL)
        };
        self.denied_call_log
            .record(path, code, &rejection, public_identity.as_ref());
        Err((code, detail))
    }

    /// Logs a call that carried no bearer credential.
    pub(super) fn record_missing_credential(&self, path: &str) {
        if let Some(skipped_denials) = self.denied_call_log.admit(Instant::now()) {
            tracing::warn!(
                path,
                code = ?Code::Unauthenticated,
                skipped_denials,
                "denied an admin call without a bearer credential"
            );
        }
    }
}

/// Logs denied admin calls at most once per interval, so a flood of attempts cannot flood the log.
#[derive(Default)]
pub(super) struct DeniedAdminCallLog {
    state: Mutex<DeniedAdminCallLogState>,
}

#[derive(Default)]
struct DeniedAdminCallLogState {
    last_logged_at: Option<Instant>,
    skipped_denials: u64,
}

impl DeniedAdminCallLog {
    fn record(
        &self,
        path: &str,
        code: Code,
        rejection: &CredentialRejection,
        public_identity: Option<&CredentialIdentity>,
    ) {
        let Some(skipped_denials) = self.admit(Instant::now()) else {
            return;
        };
        let (caller_credential, caller_issuer, caller_subject) = match public_identity {
            Some(CredentialIdentity::Jwt { issuer, subject }) => (
                "public API JWT",
                issuer.as_str(),
                subject.as_deref().unwrap_or(""),
            ),
            Some(CredentialIdentity::StaticToken) => ("public API static token", "", ""),
            None => ("unrecognized", "", ""),
        };
        tracing::warn!(
            path,
            ?code,
            reason = %rejection,
            caller_credential,
            caller_issuer,
            caller_subject,
            skipped_denials,
            "denied an admin call"
        );
    }

    /// Returns the denials skipped since the last log when this one should be logged, else `None`.
    pub(super) fn admit(&self, now: Instant) -> Option<u64> {
        let mut state = self.state.lock().unwrap_or_else(PoisonError::into_inner);
        let is_due = state.last_logged_at.is_none_or(|last_logged_at| {
            now.saturating_duration_since(last_logged_at) >= DENIED_ADMIN_CALL_LOG_INTERVAL
        });
        if !is_due {
            state.skipped_denials += 1;
            return None;
        }
        state.last_logged_at = Some(now);
        Some(std::mem::take(&mut state.skipped_denials))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn denied_call_logs_are_limited_to_one_per_interval_and_count_the_skipped_ones() {
        let log = DeniedAdminCallLog::default();
        let start = Instant::now();
        assert_eq!(log.admit(start), Some(0));
        assert_eq!(log.admit(start + Duration::from_secs(1)), None);
        assert_eq!(log.admit(start + Duration::from_secs(9)), None);
        assert_eq!(log.admit(start + DENIED_ADMIN_CALL_LOG_INTERVAL), Some(2));
        assert_eq!(
            log.admit(start + DENIED_ADMIN_CALL_LOG_INTERVAL + Duration::from_secs(1)),
            None
        );
        assert_eq!(
            log.admit(start + 2 * DENIED_ADMIN_CALL_LOG_INTERVAL),
            Some(1)
        );
    }
}
