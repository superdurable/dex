// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::error::Error;
use std::fmt::{Display, Formatter};

use dex_protocol::dex::{ErrorResponse, ErrorSubStatus as ProtoErrorSubStatus};
use prost::Message;
use prost_types::Any;
use tonic::Status;

use crate::{FlowErrorType, FlowStatus, GrpcCode};

pub type SdkResult<T> = Result<T, SdkError>;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ErrorSubStatus {
    Uncategorized,
    FlowAlreadyStarted,
    FlowNotExists,
    WorkerApiError,
    LongPollTimeout,
}

#[derive(Debug)]
pub struct ServiceError {
    code: GrpcCode,
    sub_status: ErrorSubStatus,
    detail: String,
    operation: &'static str,
    flow_id: Option<String>,
    source: Status,
}

impl ServiceError {
    pub fn code(&self) -> GrpcCode {
        self.code
    }

    pub fn sub_status(&self) -> ErrorSubStatus {
        self.sub_status
    }

    pub fn detail(&self) -> &str {
        &self.detail
    }

    pub fn operation(&self) -> &str {
        self.operation
    }

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
        }
    }
}

impl Display for ServiceError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(&self.detail)
    }
}

impl Error for ServiceError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        Some(&self.source)
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct WorkerError {
    code: Option<GrpcCode>,
    error_type: String,
    detail: String,
}

impl WorkerError {
    pub fn code(&self) -> Option<GrpcCode> {
        self.code
    }

    pub fn error_type(&self) -> &str {
        &self.error_type
    }

    pub fn detail(&self) -> &str {
        &self.detail
    }
}

#[derive(Debug)]
pub enum SdkError {
    Service {
        service: ServiceError,
    },
    FlowAlreadyStarted {
        service: ServiceError,
    },
    FlowNotFound {
        service: ServiceError,
    },
    FlowNotActive {
        service: ServiceError,
    },
    WorkerInvocation {
        service: ServiceError,
        worker: Box<WorkerError>,
    },
    RpcLockConflict {
        service: ServiceError,
    },
    LongPollTimeout {
        service: ServiceError,
    },
    FlowUncompleted {
        run_id: String,
        status: FlowStatus,
        error_type: Option<FlowErrorType>,
        message: Option<String>,
        result_count: usize,
    },
    FlowDefinition {
        message: String,
    },
    InvalidArgument {
        message: String,
    },
    ValueMapping {
        message: String,
    },
    InvalidStepResult {
        flow_type: String,
        step_type: String,
        detail: String,
    },
}

impl Display for SdkError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Service { service }
            | Self::FlowAlreadyStarted { service }
            | Self::FlowNotFound { service }
            | Self::FlowNotActive { service }
            | Self::RpcLockConflict { service }
            | Self::LongPollTimeout { service } => Display::fmt(service, formatter),
            Self::WorkerInvocation { service, .. } => Display::fmt(service, formatter),
            Self::InvalidStepResult { detail, .. } => formatter.write_str(detail),
            Self::FlowUncompleted { message, .. } => {
                formatter.write_str(message.as_deref().unwrap_or("Flow did not complete"))
            }
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
    pub fn service_error(&self) -> Option<&ServiceError> {
        match self {
            Self::Service { service }
            | Self::FlowAlreadyStarted { service }
            | Self::FlowNotFound { service }
            | Self::FlowNotActive { service }
            | Self::RpcLockConflict { service }
            | Self::LongPollTimeout { service } => Some(service),
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
        };
        if decoded.is_err() {
            return Self::Service { service };
        }
        match sub_status {
            ErrorSubStatus::FlowAlreadyStarted => Self::FlowAlreadyStarted { service },
            ErrorSubStatus::FlowNotExists => match requirement {
                FlowTargetRequirement::Active => Self::FlowNotActive { service },
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
            ErrorSubStatus::Uncategorized => Self::Service { service },
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

fn decode_details(bytes: &[u8]) -> Result<Option<ErrorResponse>, prost::DecodeError> {
    if bytes.is_empty() {
        return Ok(None);
    }
    match GoogleRpcStatus::decode(bytes) {
        Ok(status) => {
            for detail in status.details {
                if detail.type_url.ends_with("/dex.ErrorResponse") {
                    return ErrorResponse::decode(detail.value.as_slice()).map(Some);
                }
            }
            Ok(None)
        }
        Err(status_error) => ErrorResponse::decode(bytes).map(Some).or(Err(status_error)),
    }
}

fn map_sub_status(value: i32) -> ErrorSubStatus {
    match ProtoErrorSubStatus::try_from(value).ok() {
        Some(ProtoErrorSubStatus::FlowAlreadyStarted) => ErrorSubStatus::FlowAlreadyStarted,
        Some(ProtoErrorSubStatus::FlowNotExists) => ErrorSubStatus::FlowNotExists,
        Some(ProtoErrorSubStatus::WorkerApiError) => ErrorSubStatus::WorkerApiError,
        Some(ProtoErrorSubStatus::LongPollTimeOut) => ErrorSubStatus::LongPollTimeout,
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
        assert!(matches!(active, SdkError::FlowNotActive { .. }));
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
            ErrorResponse {
                detail: "service detail".to_string(),
                sub_status: sub_status as i32,
                ..ErrorResponse::default()
            },
        )
    }

    fn worker_status(code: GrpcCode) -> Status {
        status_with_response(
            code,
            ErrorResponse {
                detail: "worker failure".to_string(),
                sub_status: ProtoErrorSubStatus::WorkerApiError as i32,
                original_worker_error_detail: "invalid order".to_string(),
                original_worker_error_type: "ApplicationError".to_string(),
                original_worker_error_status: GrpcCode::InvalidArgument as i32,
            },
        )
    }

    fn status_with_response(code: GrpcCode, response: ErrorResponse) -> Status {
        let status = GoogleRpcStatus {
            code: code as i32,
            message: "gRPC detail".to_string(),
            details: vec![Any {
                type_url: "type.googleapis.com/dex.ErrorResponse".to_string(),
                value: response.encode_to_vec(),
            }],
        };
        Status::with_details(code, "gRPC detail", status.encode_to_vec().into())
    }
}
