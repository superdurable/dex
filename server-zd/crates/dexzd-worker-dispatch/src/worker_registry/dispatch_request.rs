// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::{Duration, SystemTime};

use dexzd_keyspace::PartitionId;
use dexzd_protocol::dex::{
    DispatchTaskErrorReason, InvokeExecuteMethodRequest, InvokeWaitForMethodRequest,
    InvokeWorkerRpcRequest, PollWorkerTaskFrame,
};
use dexzd_request_validation::quote_go_string;

use super::unsupported_types::{TaskKind, TaskTypeKey};

/// One ordered frame from the Worker, an assignment change, or the terminal dispatch outcome.
#[derive(Debug)]
pub enum DispatchEvent {
    /// The task was sent to this Worker, which may still reject it before any output.
    Assigned {
        /// Assigned Worker process ID.
        app_worker_id: String,
    },
    /// The assigned Worker rejected the task or disconnected before any output; matching
    /// continues with another Worker.
    Unassigned,
    /// A non-final heartbeat or stream write from the assigned Worker.
    Output(PollWorkerTaskFrame),
    /// The unique final outcome of the dispatch.
    Completed(DispatchOutcome),
}

/// The terminal result of one dispatch.
#[derive(Debug)]
pub enum DispatchOutcome {
    /// A final WaitFor, Execute, or RPC result and the Worker that served it.
    WorkerResult {
        /// Final Worker frame.
        frame: PollWorkerTaskFrame,
        /// Serving Worker process ID.
        app_worker_id: String,
    },
    /// A Worker error response and the Worker that returned it.
    WorkerError {
        /// Final Worker error frame.
        frame: PollWorkerTaskFrame,
        /// Serving Worker process ID.
        app_worker_id: String,
    },
    /// A matching failure with the same reason values as DispatchTaskErrorReason.
    DispatchError {
        /// Matching failure reason.
        reason: DispatchTaskErrorReason,
        /// Human-readable diagnostic.
        detail: String,
    },
    /// The caller cancelled this dispatch.
    Cancelled,
    /// The method deadline elapsed before a final Worker frame.
    DeadlineExceeded,
    /// The matching owner shut down while waiting for a Worker, or its node lease is no longer
    /// valid.
    ServerClosing,
}

/// The unmodified Worker request carried by a pull task.
#[derive(Debug, Clone)]
pub enum DispatchTaskPayload {
    /// Runs a Step's WaitFor method.
    WaitFor(InvokeWaitForMethodRequest),
    /// Runs a Step's Execute method.
    Execute(InvokeExecuteMethodRequest),
    /// Invokes a Worker RPC.
    Rpc(InvokeWorkerRpcRequest),
}

/// Inputs for one server-generated pull task.
#[derive(Debug, Clone)]
pub struct DispatchRequest {
    /// Tenant containing the Flow and its Worker queues; use zero for the default tenant.
    pub tenant_id: u64,
    /// Flow instance ID used for same-Flow affinity.
    pub flow_id: String,
    /// Flow type queue used after an optional sticky Worker search.
    pub flow_type: String,
    /// Worker process ID preferred while the sticky wait lasts.
    pub sticky_app_worker_id: Option<String>,
    /// Worker request sent without modifying its application payload.
    pub payload: DispatchTaskPayload,
    /// Method deadline, including Worker processing time.
    pub deadline: SystemTime,
    /// Override for the 5-second sticky wait; zero skips the sticky search.
    pub sticky_wait: Option<Duration>,
    /// Override for the 10-second total Worker search.
    pub dispatch_wait: Option<Duration>,
    /// Partition of the Flow; removing the partition ends the search while it waits.
    pub partition: Option<PartitionId>,
}

impl DispatchRequest {
    /// Returns the task type the payload runs, as the unsupported type cache names it.
    pub(super) fn task_type(&self) -> TaskTypeKey {
        let (kind, name) = match &self.payload {
            DispatchTaskPayload::WaitFor(value) => (TaskKind::Step, &value.step_type),
            DispatchTaskPayload::Execute(value) => (TaskKind::Step, &value.step_type),
            DispatchTaskPayload::Rpc(value) => (TaskKind::Rpc, &value.rpc_name),
        };
        TaskTypeKey {
            tenant_id: self.tenant_id,
            flow_type: self.flow_type.clone(),
            kind,
            name: name.clone(),
        }
    }

    pub(super) fn unknown_name_detail(&self) -> String {
        match &self.payload {
            DispatchTaskPayload::WaitFor(value) => format!(
                "no pull Worker registered Step {} for Flow type {}",
                quote_go_string(&value.step_type),
                quote_go_string(&value.flow_type)
            ),
            DispatchTaskPayload::Execute(value) => format!(
                "no pull Worker registered Step {} for Flow type {}",
                quote_go_string(&value.step_type),
                quote_go_string(&value.flow_type)
            ),
            DispatchTaskPayload::Rpc(value) => format!(
                "no pull Worker registered RPC {} for Flow type {}",
                quote_go_string(&value.rpc_name),
                quote_go_string(&value.flow_type)
            ),
        }
    }
}
