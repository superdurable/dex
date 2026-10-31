// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::error::Error;
use std::fmt::{Display, Formatter};

use dex_protocol::dex::{ErrorSubStatus as ProtoErrorSubStatus, ServiceErrorResponse};
use prost::Message;
use prost_types::Any;
use tonic::Status;

use crate::GrpcCode;

/// Result returned by fallible Dex SDK operations.
pub type SdkResult<T> = Result<T, SdkError>;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
/// Provides Dex-specific detail beyond a gRPC status code.
#[non_exhaustive]
pub enum ErrorSubStatus {
    /// Dex returned no specific status.
    Uncategorized,
    /// A start request targeted an already-existing Flow ID.
    FlowAlreadyStarted,
    /// The requested Flow does not exist or is no longer active.
    FlowNotExists,
    /// A Worker Step or RPC invocation failed.
    WorkerApiError,
    /// A long-poll deadline elapsed while the Flow remained active.
    LongPollTimeout,
    /// A pending Channel message ID no longer exists.
    ChannelMessageNotFound,
    /// A caller-defined request budget expired.
    RequestTimeout,
    /// Dex rejected an update-class request during Flow close cleanup.
    FlowClosing,
}

#[derive(Clone, Debug)]
/// Preserves one failed Dex service operation and its transport source.
pub struct ServiceError {
    code: GrpcCode,
    sub_status: ErrorSubStatus,
    detail: String,
    operation: &'static str,
    flow_id: Option<String>,
    source: Status,
    pull_registration_error: Option<Box<crate::PullRegistrationError>>,
    call_context: Option<String>,
}

impl ServiceError {
    /// Returns the outer gRPC status code.
    pub fn code(&self) -> GrpcCode {
        self.code
    }

    /// Returns the decoded Dex-specific status.
    pub fn sub_status(&self) -> ErrorSubStatus {
        self.sub_status
    }

    /// Returns the most specific server or transport detail available.
    pub fn detail(&self) -> &str {
        &self.detail
    }

    /// Returns the SDK operation name that failed.
    pub fn operation(&self) -> &str {
        self.operation
    }

    /// Returns the targeted Flow ID when the operation had one.
    pub fn flow_id(&self) -> Option<&str> {
        self.flow_id.as_deref()
    }

    pub(crate) fn local(operation: &'static str, detail: impl Into<String>) -> Self {
        let detail = detail.into();
        Self {
            code: GrpcCode::Unknown,
            sub_status: ErrorSubStatus::Uncategorized,
            detail: detail.clone(),
            operation,
            flow_id: None,
            source: Status::unknown(detail),
            pull_registration_error: None,
            call_context: None,
        }
    }
}

impl Display for ServiceError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match &self.call_context {
            Some(call_context) => write!(formatter, "{call_context}: {}", self.detail),
            None => formatter.write_str(&self.detail),
        }
    }
}

impl Error for ServiceError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match &self.pull_registration_error {
            Some(rejection) => Some(rejection.as_ref()),
            None => Some(&self.source),
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// Preserves the original Worker-side error carried by a service failure.
pub struct WorkerError {
    code: Option<GrpcCode>,
    error_type: String,
    detail: String,
}

impl WorkerError {
    /// Returns the Worker gRPC status when available.
    pub fn code(&self) -> Option<GrpcCode> {
        self.code
    }

    /// Returns the Worker exception or error type name.
    pub fn error_type(&self) -> &str {
        &self.error_type
    }

    /// Returns the Worker-provided failure detail.
    pub fn detail(&self) -> &str {
        &self.detail
    }
}

#[derive(Clone, Debug)]
/// Reports service, definition, value, handler, and Flow-completion failures.
///
/// Match specific variants when recovery differs. [`Self::service_error`] extracts common gRPC
/// metadata from every service-backed variant.
#[non_exhaustive]
pub enum SdkError {
    /// A general Dex service or transport call failed.
    Service {
        /// Structured service failure.
        service: ServiceError,
    },
    /// Starting a Flow conflicted with an existing Flow ID.
    FlowAlreadyStarted {
        /// Structured start-operation failure.
        service: ServiceError,
    },
    /// An update-class request arrived while Dex was closing the Flow.
    ///
    /// Re-read the retained terminal Flow state before deciding what to do next.
    /// Match this variant instead of comparing service detail text:
    ///
    /// ```no_run
    /// # use dex_sdk::{SdkError, SdkResult};
    /// fn should_reread_terminal_state(result: &SdkResult<()>) -> bool {
    ///     matches!(result, Err(SdkError::FlowClosing { .. }))
    /// }
    /// ```
    FlowClosing {
        /// Structured rejected-operation failure, including the original transport cause.
        service: ServiceError,
    },
    /// An operation requiring an existing Flow targeted an unknown ID.
    FlowNotFound {
        /// Structured lookup failure.
        service: ServiceError,
    },
    /// A Flow was not found or the operation could not use its closed execution.
    ///
    /// RPC calls also use this variant for missing query-only targets. A query-only RPC without
    /// locks, transactionality, durable effects, or Server-forced Update routing can read a retained
    /// terminal execution. At that confirmed read boundary, treat this variant as not found without
    /// a lifecycle probe. Preserve other service and Worker failures.
    ///
    /// For mutations and active-only RPCs, this variant does not distinguish missing from closed
    /// targets or prove that the requested action succeeded. Match it as follows:
    ///
    /// ```no_run
    /// # use dex_sdk::{SdkError, SdkResult};
    /// fn report_unavailable(result: SdkResult<()>) -> SdkResult<()> {
    ///     match result {
    ///         Err(SdkError::FlowNotActiveOrNotFound { service }) => {
    ///             eprintln!("{} has no usable target", service.operation());
    ///             Err(SdkError::FlowNotActiveOrNotFound { service })
    ///         }
    ///         other => other,
    ///     }
    /// }
    /// ```
    FlowNotActiveOrNotFound {
        /// Structured target failure, including the original transport cause.
        service: ServiceError,
    },
    /// A Worker Step or RPC handler failed.
    WorkerInvocation {
        /// Outer Dex service failure.
        service: ServiceError,
        /// Original Worker-side failure metadata.
        worker: Box<WorkerError>,
    },
    /// A locking RPC could not acquire all requested Attribute locks.
    RpcLockConflict {
        /// Structured aborted-operation failure.
        service: ServiceError,
    },
    /// A long-poll timeout elapsed without indicating a Flow failure.
    LongPollTimeout {
        /// Structured timeout failure.
        service: ServiceError,
    },
    /// A requested Channel message is no longer pending.
    ChannelMessageNotFound {
        /// Structured deletion failure.
        service: ServiceError,
    },
    /// A caller-defined request budget expired.
    RequestTimeout {
        /// Structured timeout failure.
        service: ServiceError,
    },
    /// A registered Flow, Step, RPC, or persistence definition is invalid.
    FlowDefinition {
        /// Developer-actionable contract violation.
        message: String,
    },
    /// An SDK method received an invalid argument.
    InvalidArgument {
        /// Developer-actionable argument violation.
        message: String,
    },
    /// An application value could not be serialized or decoded.
    ValueMapping {
        /// Developer-actionable mapping failure.
        message: String,
    },
    /// A Step returned a value that violates its handler contract.
    InvalidStepResult {
        /// Flow type containing the Step.
        flow_type: String,
        /// Step type that returned the invalid value.
        step_type: String,
        /// Developer-actionable result violation.
        detail: String,
    },
}

impl Display for SdkError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Service { service }
            | Self::FlowAlreadyStarted { service }
            | Self::FlowClosing { service }
            | Self::FlowNotFound { service }
            | Self::FlowNotActiveOrNotFound { service }
            | Self::RpcLockConflict { service }
            | Self::LongPollTimeout { service }
            | Self::ChannelMessageNotFound { service } => Display::fmt(service, formatter),
            Self::RequestTimeout { service } => Display::fmt(service, formatter),
            Self::WorkerInvocation { service, .. } => Display::fmt(service, formatter),
            Self::InvalidStepResult { detail, .. } => formatter.write_str(detail),
            Self::FlowDefinition { message }
            | Self::InvalidArgument { message }
            | Self::ValueMapping { message } => formatter.write_str(message),
        }
    }
}

impl Error for SdkError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        self.service_error()
            .map(|error| error as &(dyn Error + 'static))
    }
}

impl SdkError {
    /// Returns the typed pull queue rejection when a permanent error leaves no registered stream.
    ///
    /// Registration waiters and opt-in Worker shutdown return the existing [`Self::Service`]
    /// variant. This accessor preserves its queue name and original gRPC status. Other errors
    /// return `None`. See [`crate::PullRegistrationError`] for a complete inspection example.
    pub fn pull_registration_error(&self) -> Option<&crate::PullRegistrationError> {
        self.service_error()
            .and_then(|service| service.pull_registration_error.as_deref())
    }

    pub(crate) fn from_pull_registration(rejection: crate::PullRegistrationError) -> Self {
        Self::Service {
            service: ServiceError {
                code: rejection.status.code(),
                sub_status: ErrorSubStatus::Uncategorized,
                detail: rejection.to_string(),
                operation: "pull_worker_registration",
                flow_id: None,
                source: rejection.status.clone(),
                pull_registration_error: Some(Box::new(rejection)),
                call_context: None,
            },
        }
    }

    /// Returns shared service metadata for service-backed variants.
    ///
    /// Local definition, argument, mapping, and invalid-result errors return `None`.
    pub fn service_error(&self) -> Option<&ServiceError> {
        match self {
            Self::Service { service }
            | Self::FlowAlreadyStarted { service }
            | Self::FlowClosing { service }
            | Self::FlowNotFound { service }
            | Self::FlowNotActiveOrNotFound { service }
            | Self::RpcLockConflict { service }
            | Self::LongPollTimeout { service }
            | Self::ChannelMessageNotFound { service } => Some(service),
            Self::RequestTimeout { service } => Some(service),
            Self::WorkerInvocation { service, .. } => Some(service),
            _ => None,
        }
    }

    pub(crate) fn from_status(
        status: Status,
        operation: &'static str,
        flow_id: Option<&str>,
        requirement: FlowTargetRequirement,
    ) -> Self {
        let code = status.code();
        let decoded = decode_details(status.details());
        let details = decoded.as_ref().ok().and_then(|details| details.as_ref());
        let detail = details
            .filter(|details| !details.detail.is_empty())
            .map_or_else(
                || {
                    if status.message().is_empty() {
                        format!("{code:?}")
                    } else {
                        status.message().to_string()
                    }
                },
                |details| details.detail.clone(),
            );
        let sub_status = details
            .map(|details| map_sub_status(details.sub_status))
            .unwrap_or(ErrorSubStatus::Uncategorized);
        let service = ServiceError {
            code,
            sub_status,
            detail,
            operation,
            flow_id: flow_id.map(str::to_string),
            source: status,
            pull_registration_error: None,
            call_context: None,
        };
        if decoded.is_err() {
            return Self::Service { service };
        }
        match sub_status {
            ErrorSubStatus::FlowAlreadyStarted => Self::FlowAlreadyStarted { service },
            ErrorSubStatus::FlowClosing => Self::FlowClosing { service },
            ErrorSubStatus::FlowNotExists => match requirement {
                FlowTargetRequirement::Active => Self::FlowNotActiveOrNotFound { service },
                FlowTargetRequirement::Existing => Self::FlowNotFound { service },
                FlowTargetRequirement::None => Self::Service { service },
            },
            ErrorSubStatus::WorkerApiError if code == GrpcCode::Aborted => {
                Self::RpcLockConflict { service }
            }
            ErrorSubStatus::WorkerApiError => {
                let details = details.expect("worker details were decoded");
                Self::WorkerInvocation {
                    service,
                    worker: Box::new(WorkerError {
                        code: (details.original_worker_error_status != 0)
                            .then(|| GrpcCode::from_i32(details.original_worker_error_status)),
                        error_type: details.original_worker_error_type.clone(),
                        detail: details.original_worker_error_detail.clone(),
                    }),
                }
            }
            ErrorSubStatus::LongPollTimeout => Self::LongPollTimeout { service },
            ErrorSubStatus::ChannelMessageNotFound => Self::ChannelMessageNotFound { service },
            ErrorSubStatus::RequestTimeout => Self::RequestTimeout { service },
            ErrorSubStatus::Uncategorized => Self::Service { service },
        }
    }

    pub(crate) fn request_timeout(operation: &'static str, flow_id: &str) -> Self {
        let detail = "request timed out".to_string();
        Self::RequestTimeout {
            service: ServiceError {
                code: GrpcCode::DeadlineExceeded,
                sub_status: ErrorSubStatus::RequestTimeout,
                detail: detail.clone(),
                operation,
                flow_id: Some(flow_id.to_string()),
                source: Status::deadline_exceeded(detail),
                pull_registration_error: None,
                call_context: None,
            },
        }
    }

    /// Returns the target-specific variant for a missing Flow reported under
    /// [`FlowTargetRequirement::None`], and returns every other error unchanged.
    pub(crate) fn with_flow_target_requirement(self, requirement: FlowTargetRequirement) -> Self {
        match self {
            Self::Service { service } if service.sub_status == ErrorSubStatus::FlowNotExists => {
                match requirement {
                    FlowTargetRequirement::Active => Self::FlowNotActiveOrNotFound { service },
                    FlowTargetRequirement::Existing => Self::FlowNotFound { service },
                    FlowTargetRequirement::None => Self::Service { service },
                }
            }
            other => other,
        }
    }

    /// Returns the same variant with `call_context` before its message and, for service-backed
    /// variants, with `operation` and `flow_id` as its service metadata.
    pub(crate) fn with_call_context(
        self,
        operation: &'static str,
        flow_id: &str,
        call_context: String,
    ) -> Self {
        let with_service_context = |mut service: ServiceError| {
            service.operation = operation;
            service.flow_id = Some(flow_id.to_string());
            service.call_context = Some(call_context.clone());
            service
        };
        let with_message_context = |message: String| format!("{call_context}: {message}");
        match self {
            Self::Service { service } => Self::Service {
                service: with_service_context(service),
            },
            Self::FlowAlreadyStarted { service } => Self::FlowAlreadyStarted {
                service: with_service_context(service),
            },
            Self::FlowClosing { service } => Self::FlowClosing {
                service: with_service_context(service),
            },
            Self::FlowNotFound { service } => Self::FlowNotFound {
                service: with_service_context(service),
            },
            Self::FlowNotActiveOrNotFound { service } => Self::FlowNotActiveOrNotFound {
                service: with_service_context(service),
            },
            Self::WorkerInvocation { service, worker } => Self::WorkerInvocation {
                service: with_service_context(service),
                worker,
            },
            Self::RpcLockConflict { service } => Self::RpcLockConflict {
                service: with_service_context(service),
            },
            Self::LongPollTimeout { service } => Self::LongPollTimeout {
                service: with_service_context(service),
            },
            Self::ChannelMessageNotFound { service } => Self::ChannelMessageNotFound {
                service: with_service_context(service),
            },
            Self::RequestTimeout { service } => Self::RequestTimeout {
                service: with_service_context(service),
            },
            Self::FlowDefinition { message } => Self::FlowDefinition {
                message: with_message_context(message),
            },
            Self::InvalidArgument { message } => Self::InvalidArgument {
                message: with_message_context(message),
            },
            Self::ValueMapping { message } => Self::ValueMapping {
                message: with_message_context(message),
            },
            Self::InvalidStepResult {
                flow_type,
                step_type,
                detail,
            } => Self::InvalidStepResult {
                flow_type,
                step_type,
                detail: with_message_context(detail),
            },
        }
    }
}

#[derive(Clone, Copy)]
pub(crate) enum FlowTargetRequirement {
    None,
    Existing,
    Active,
}

#[derive(Clone, PartialEq, Message)]
struct GoogleRpcStatus {
    #[prost(int32, tag = "1")]
    code: i32,
    #[prost(string, tag = "2")]
    message: String,
    #[prost(message, repeated, tag = "3")]
    details: Vec<Any>,
}

fn decode_details(bytes: &[u8]) -> Result<Option<ServiceErrorResponse>, prost::DecodeError> {
    if bytes.is_empty() {
        return Ok(None);
    }
    match GoogleRpcStatus::decode(bytes) {
        Ok(status) => {
            for detail in status.details {
                if detail.type_url.ends_with("/dex.ServiceErrorResponse") {
                    return ServiceErrorResponse::decode(detail.value.as_slice()).map(Some);
                }
            }
            Ok(None)
        }
        Err(status_error) => ServiceErrorResponse::decode(bytes)
            .map(Some)
            .or(Err(status_error)),
    }
}

fn map_sub_status(value: i32) -> ErrorSubStatus {
    match ProtoErrorSubStatus::try_from(value).ok() {
        Some(ProtoErrorSubStatus::FlowAlreadyStarted) => ErrorSubStatus::FlowAlreadyStarted,
        Some(ProtoErrorSubStatus::FlowNotExists) => ErrorSubStatus::FlowNotExists,
        Some(ProtoErrorSubStatus::WorkerApiError) => ErrorSubStatus::WorkerApiError,
        Some(ProtoErrorSubStatus::LongPollTimeOut) => ErrorSubStatus::LongPollTimeout,
        Some(ProtoErrorSubStatus::ChannelMessageNotFound) => ErrorSubStatus::ChannelMessageNotFound,
        Some(ProtoErrorSubStatus::RequestTimeout) => ErrorSubStatus::RequestTimeout,
        Some(ProtoErrorSubStatus::FlowClosing) => ErrorSubStatus::FlowClosing,
        _ => ErrorSubStatus::Uncategorized,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn missing_flow_uses_endpoint_lifecycle_requirement() {
        let found = SdkError::from_status(
            rich_status(GrpcCode::NotFound, ProtoErrorSubStatus::FlowNotExists),
            "describe_flow",
            Some("flow-id"),
            FlowTargetRequirement::Existing,
        );
        assert!(matches!(found, SdkError::FlowNotFound { .. }));

        let active = SdkError::from_status(
            rich_status(GrpcCode::NotFound, ProtoErrorSubStatus::FlowNotExists),
            "publish",
            Some("flow-id"),
            FlowTargetRequirement::Active,
        );
        assert!(matches!(active, SdkError::FlowNotActiveOrNotFound { .. }));
    }

    #[test]
    fn call_context_names_the_call_without_changing_the_variant_or_detail() {
        let missing = SdkError::from_status(
            rich_status(GrpcCode::NotFound, ProtoErrorSubStatus::FlowNotExists),
            "load_blobs",
            None,
            FlowTargetRequirement::None,
        )
        .with_call_context(
            "get_attributes",
            "flow-id",
            "get_attributes Flow \"flow-id\"".to_string(),
        );
        assert!(matches!(missing, SdkError::Service { .. }));
        let service = missing.service_error().expect("service metadata");
        assert_eq!("service detail", service.detail());
        assert_eq!("get_attributes", service.operation());
        assert_eq!(Some("flow-id"), service.flow_id());
        assert_eq!(
            "get_attributes Flow \"flow-id\": service detail",
            missing.to_string()
        );

        let mapping = SdkError::ValueMapping {
            message: "blob cache failed".to_string(),
        }
        .with_call_context("get_attributes", "flow-id", "context".to_string());
        assert!(
            matches!(mapping, SdkError::ValueMapping { message } if message == "context: blob cache failed")
        );
    }

    #[test]
    fn missing_flow_during_load_blobs_maps_to_the_caller_target() {
        let load_blobs_error = || {
            SdkError::from_status(
                rich_status(GrpcCode::NotFound, ProtoErrorSubStatus::FlowNotExists),
                "load_blobs",
                None,
                FlowTargetRequirement::None,
            )
        };
        assert!(matches!(load_blobs_error(), SdkError::Service { .. }));
        assert!(matches!(
            load_blobs_error().with_flow_target_requirement(FlowTargetRequirement::Existing),
            SdkError::FlowNotFound { .. }
        ));
        let unrelated = SdkError::from_status(
            Status::internal("broken"),
            "load_blobs",
            None,
            FlowTargetRequirement::None,
        )
        .with_flow_target_requirement(FlowTargetRequirement::Existing);
        assert!(matches!(unrelated, SdkError::Service { .. }));
    }

    #[test]
    fn flow_closing_has_a_typed_variant() {
        let error = SdkError::from_status(
            rich_status(
                GrpcCode::FailedPrecondition,
                ProtoErrorSubStatus::FlowClosing,
            ),
            "wait_for_attribute_match",
            Some("flow-id"),
            FlowTargetRequirement::Active,
        );
        assert!(matches!(error, SdkError::FlowClosing { .. }));
        let service = error
            .service_error()
            .expect("FlowClosing carries service metadata");
        assert_eq!(service.sub_status(), ErrorSubStatus::FlowClosing);
        assert_eq!(service.code(), GrpcCode::FailedPrecondition);
    }

    #[test]
    fn worker_details_and_lock_conflicts_are_distinct() {
        let invocation = SdkError::from_status(
            worker_status(GrpcCode::FailedPrecondition),
            "invoke_rpc",
            Some("flow-id"),
            FlowTargetRequirement::Active,
        );
        match invocation {
            SdkError::WorkerInvocation { service, worker } => {
                assert_eq!(GrpcCode::FailedPrecondition, service.code());
                assert_eq!(Some(GrpcCode::InvalidArgument), worker.code());
                assert_eq!("ApplicationError", worker.error_type());
                assert_eq!("invalid order", worker.detail());
            }
            error => panic!("expected WorkerInvocation, got {error:?}"),
        }

        let conflict = SdkError::from_status(
            worker_status(GrpcCode::Aborted),
            "invoke_rpc",
            Some("flow-id"),
            FlowTargetRequirement::Active,
        );
        assert!(matches!(conflict, SdkError::RpcLockConflict { .. }));
    }

    #[test]
    fn missing_and_malformed_details_use_generic_fallback() {
        let missing = SdkError::from_status(
            Status::internal("missing"),
            "search_flows",
            None,
            FlowTargetRequirement::None,
        );
        assert!(matches!(missing, SdkError::Service { .. }));

        let malformed = SdkError::from_status(
            Status::with_details(GrpcCode::Internal, "malformed", vec![255].into()),
            "search_flows",
            None,
            FlowTargetRequirement::None,
        );
        assert!(matches!(malformed, SdkError::Service { .. }));
    }

    fn rich_status(code: GrpcCode, sub_status: ProtoErrorSubStatus) -> Status {
        status_with_response(
            code,
            ServiceErrorResponse {
                detail: "service detail".to_string(),
                sub_status: sub_status as i32,
                ..ServiceErrorResponse::default()
            },
        )
    }

    fn worker_status(code: GrpcCode) -> Status {
        status_with_response(
            code,
            ServiceErrorResponse {
                detail: "worker failure".to_string(),
                sub_status: ProtoErrorSubStatus::WorkerApiError as i32,
                original_worker_error_detail: "invalid order".to_string(),
                original_worker_error_type: "ApplicationError".to_string(),
                original_worker_error_status: GrpcCode::InvalidArgument as i32,
                original_worker_error_stack_trace: String::new(),
                ..ServiceErrorResponse::default()
            },
        )
    }

    fn status_with_response(code: GrpcCode, response: ServiceErrorResponse) -> Status {
        let status = GoogleRpcStatus {
            code: code as i32,
            message: "gRPC detail".to_string(),
            details: vec![Any {
                type_url: "type.googleapis.com/dex.ServiceErrorResponse".to_string(),
                value: response.encode_to_vec(),
            }],
        };
        Status::with_details(code, "gRPC detail", status.encode_to_vec().into())
    }
}
