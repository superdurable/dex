// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::error::Error;
use std::fmt::{Display, Formatter};

use crate::{FlowErrorType, FlowStatus};

pub type HandlerResult<T> = Result<T, HandlerError>;
pub type SdkResult<T> = Result<T, SdkError>;

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct HandlerError {
    message: String,
}

impl HandlerError {
    pub fn new(message: impl Into<String>) -> Self {
        Self {
            message: message.into(),
        }
    }
}

impl Display for HandlerError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(&self.message)
    }
}

impl Error for HandlerError {}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum SdkError {
    NotImplemented(&'static str),
    FlowUncompleted {
        run_id: String,
        status: FlowStatus,
        error_type: Option<FlowErrorType>,
        message: Option<String>,
        result_count: usize,
    },
}

impl Display for SdkError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::NotImplemented(component) => write!(formatter, "{component} is not implemented"),
            Self::FlowUncompleted { message, .. } => {
                formatter.write_str(message.as_deref().unwrap_or("Flow did not complete"))
            }
        }
    }
}

impl Error for SdkError {}
