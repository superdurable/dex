// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

//! Strongly typed Rust client and worker APIs for durable Dex Flows.
//!
//! Define [`Flow`] and [`Step`] implementations, register them with [`Registry`], host them with
//! [`Worker`], and control executions with [`Client`]. The crate also exposes typed Attributes,
//! Channels, waits, decisions, RPCs, lifecycle options, and structured errors.

#![deny(missing_docs)]

mod attribute;
mod channel;
mod client;
mod client_options;
mod context;
mod flow;
mod flow_config;
mod flow_info;
mod flow_result;
mod flow_timeout_handler_options;
mod handler_error;
mod persistence;
mod registry;
mod retry_policy;
mod rpc;
mod sdk_error;
mod start_flow_options;
mod step;
mod step_execution;
mod step_options;
mod stop_flow_options;
mod stream;
mod sub_flow;
mod time_travel_options;
mod timer;
mod value;
mod value_hydrator;
mod value_mapper;
mod wait;
mod worker;
mod worker_dispatcher;
mod worker_options;
mod worker_output;

pub use attribute::{Attribute, AttributeIndex, AttributeMap, AttributeMapLoad};
pub use channel::{Channel, ChannelGuard, ChannelMap, ChannelMapLoad, ChannelMessage};
pub use client::Client;
pub use client_options::ClientOptions;
pub use context::{Context, RecoveryErrorInfo};
pub use dex_blob_cache::{BlobCache, BlobCacheConfig};
pub use flow::{Flow, FlowTimeoutHandler};
pub use flow_config::{ActiveStepSearchMode, FlowConfig};
pub use flow_info::{FlowErrorType, FlowInfo, FlowStatus, SearchFlowEntry, SearchFlowsPage};
pub use flow_result::{FlowResult, StepCompletion};
pub use flow_timeout_handler_options::{FlowTimeoutHandlerFailure, FlowTimeoutHandlerOptions};
pub use handler_error::{HandlerError, HandlerResult};
pub use persistence::PersistenceSchema;
pub use registry::Registry;
pub use retry_policy::RetryPolicy;
pub use rpc::{Rpc, RpcDefinition, RpcList, RpcResult};
pub use sdk_error::{ErrorSubStatus, SdkError, SdkResult, ServiceError, WorkerError};
pub use start_flow_options::{FlowTimeoutPolicy, IdReusePolicy, StartFlowOptions};
pub use step::{Step, StepDecision, StepList, StepMovement};
pub use step_execution::StepExecutionId;
pub use step_options::{StepDurability, StepOptions, WaitForFailurePolicy};
pub use stop_flow_options::StopFlowOptions;
pub use stream::{BufferedTextStream, BufferedTextStreamOptions, Stream, StreamMessage};
pub use sub_flow::{SubFlow, SubFlowOptions, SubFlowReusePolicy};
pub use time_travel_options::{TimeTravelOptions, TimeTravelStepMethod};
pub use timer::{Timer, TimerId};
pub use tonic::Code as GrpcCode;
pub use value::Value;
pub use wait::{Condition, ConditionCombination, Wait};
pub use worker::Worker;
pub use worker_options::{WorkerOptions, WorkerTarget};

pub(crate) use value::short_type_name;
