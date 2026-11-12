// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use object_store::client::HttpErrorKind;

use super::response_status_classifier::{HttpFailureCause, http_failure_cause};
use crate::{ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation};

/// Whether a failed call could have changed the stored object.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum RequestEffect {
    /// A read, which changes nothing.
    Read,
    /// A write or delete, which may have taken effect when its outcome is lost.
    Write,
}

/// Maps a failure that is not a precondition result to an [`ObjectStoreError`].
pub(crate) fn map_object_store_error(
    operation: ObjectStoreOperation,
    key: &str,
    effect: RequestEffect,
    error: &object_store::Error,
) -> ObjectStoreError {
    let kind = match error {
        object_store::Error::PermissionDenied { .. }
        | object_store::Error::Unauthenticated { .. }
        | object_store::Error::NotModified { .. }
        | object_store::Error::NotFound { .. }
        | object_store::Error::InvalidPath { .. }
        | object_store::Error::NotSupported { .. }
        | object_store::Error::NotImplemented { .. }
        | object_store::Error::UnknownConfigurationKey { .. } => ObjectStoreErrorKind::Permanent,
        // A rejected precondition or conflict means the request did not take effect.
        object_store::Error::AlreadyExists { .. } | object_store::Error::Precondition { .. } => {
            ObjectStoreErrorKind::Transient
        }
        _ => cause_kind(http_failure_cause(error), effect),
    };
    ObjectStoreError::new(operation, key, kind, error.to_string())
}

/// Returns true when the service throttled the request, which therefore did not take effect.
pub(crate) fn is_throttled(error: &object_store::Error) -> bool {
    matches!(
        http_failure_cause(error),
        HttpFailureCause::Status(status) if status.status == 429
    )
}

/// Returns the status of a response that the backend classified, if `error` has one.
pub(crate) fn classified_status(error: &object_store::Error) -> Option<u16> {
    match http_failure_cause(error) {
        HttpFailureCause::Status(status) => Some(status.status),
        HttpFailureCause::Transport(_) | HttpFailureCause::NotHttp => None,
    }
}

fn cause_kind(cause: HttpFailureCause, effect: RequestEffect) -> ObjectStoreErrorKind {
    match (cause, effect) {
        (HttpFailureCause::Status(status), _) if status.status == 429 => {
            ObjectStoreErrorKind::Transient
        }
        (HttpFailureCause::Status(status), RequestEffect::Write)
            if status.is_pending_copy() || is_server_error_or_timeout(status.status) =>
        {
            ObjectStoreErrorKind::OutcomeUnknown
        }
        (HttpFailureCause::Status(status), RequestEffect::Read)
            if is_server_error_or_timeout(status.status) =>
        {
            ObjectStoreErrorKind::Transient
        }
        (HttpFailureCause::Status(_), _) => ObjectStoreErrorKind::Permanent,
        // Only a failed connection proves the request was never sent.
        (HttpFailureCause::Transport(HttpErrorKind::Connect), _) => ObjectStoreErrorKind::Transient,
        (HttpFailureCause::Transport(_), RequestEffect::Write) => {
            ObjectStoreErrorKind::OutcomeUnknown
        }
        (HttpFailureCause::Transport(_), RequestEffect::Read) => ObjectStoreErrorKind::Transient,
        // A response that failed to parse may follow a write that took effect.
        (HttpFailureCause::NotHttp, RequestEffect::Write) => ObjectStoreErrorKind::OutcomeUnknown,
        (HttpFailureCause::NotHttp, RequestEffect::Read) => ObjectStoreErrorKind::Permanent,
    }
}

fn is_server_error_or_timeout(status: u16) -> bool {
    status == 408 || (500..=599).contains(&status)
}
