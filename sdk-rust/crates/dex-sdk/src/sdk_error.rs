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

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum SdkError {
    Service {
        code: GrpcCode,
        sub_status: ErrorSubStatus,
        detail: String,
    },
    FlowAlreadyStarted {
        code: GrpcCode,
        detail: String,
    },
    FlowNotFound {
        code: GrpcCode,
        detail: String,
    },
    FlowNotActive {
        code: GrpcCode,
        detail: String,
    },
    WorkerInvocation {
        code: GrpcCode,
        detail: String,
        worker_error_type: String,
        worker_error_detail: String,
        worker_code: Option<GrpcCode>,
    },
    RpcLockConflict {
        detail: String,
    },
    LongPollTimeout {
        code: GrpcCode,
        flow_id: String,
        detail: String,
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
            Self::Service { detail, .. }
            | Self::FlowAlreadyStarted { detail, .. }
            | Self::FlowNotFound { detail, .. }
            | Self::FlowNotActive { detail, .. }
            | Self::WorkerInvocation { detail, .. }
            | Self::RpcLockConflict { detail }
            | Self::LongPollTimeout { detail, .. }
            | Self::InvalidStepResult { detail, .. } => formatter.write_str(detail),
            Self::FlowUncompleted { message, .. } => {
                formatter.write_str(message.as_deref().unwrap_or("Flow did not complete"))
            }
            Self::FlowDefinition { message }
            | Self::InvalidArgument { message }
            | Self::ValueMapping { message } => formatter.write_str(message),
        }
    }
}

impl Error for SdkError {}

impl SdkError {
    pub(crate) fn from_status(status: Status) -> Self {
        let code = status.code();
        let details = decode_details(status.details());
        let detail = details
            .as_ref()
            .filter(|value| !value.detail.is_empty())
            .map_or_else(
                || status.message().to_string(),
                |value| value.detail.clone(),
            );
        let sub_status = details
            .as_ref()
            .map(|value| map_sub_status(value.sub_status))
            .unwrap_or(ErrorSubStatus::Uncategorized);
        if code == GrpcCode::Aborted {
            return Self::RpcLockConflict { detail };
        }
        match sub_status {
            ErrorSubStatus::FlowAlreadyStarted => Self::FlowAlreadyStarted { code, detail },
            ErrorSubStatus::FlowNotExists => Self::FlowNotFound { code, detail },
            ErrorSubStatus::WorkerApiError => {
                let details = details.unwrap_or_default();
                Self::WorkerInvocation {
                    code,
                    detail,
                    worker_error_type: details.original_worker_error_type,
                    worker_error_detail: details.original_worker_error_detail,
                    worker_code: (details.original_worker_error_status != 0)
                        .then(|| GrpcCode::from_i32(details.original_worker_error_status)),
                }
            }
            ErrorSubStatus::LongPollTimeout => Self::LongPollTimeout {
                code,
                flow_id: String::new(),
                detail,
            },
            ErrorSubStatus::Uncategorized => Self::Service {
                code,
                sub_status,
                detail,
            },
        }
    }
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

fn decode_details(bytes: &[u8]) -> Option<ErrorResponse> {
    if bytes.is_empty() {
        return None;
    }
    if let Ok(status) = GoogleRpcStatus::decode(bytes) {
        for detail in status.details {
            if detail.type_url.ends_with("/dex.ErrorResponse")
                && let Ok(error) = ErrorResponse::decode(detail.value.as_slice())
            {
                return Some(error);
            }
        }
    }
    ErrorResponse::decode(bytes).ok()
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
