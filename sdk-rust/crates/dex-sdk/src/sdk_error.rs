// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::error::Error;
use std::fmt::{Display, Formatter};

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
    NotImplemented(&'static str),
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
            Self::NotImplemented(component) => write!(formatter, "{component} is not implemented"),
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
            Self::FlowDefinition { message } | Self::ValueMapping { message } => {
                formatter.write_str(message)
            }
        }
    }
}

impl Error for SdkError {}
