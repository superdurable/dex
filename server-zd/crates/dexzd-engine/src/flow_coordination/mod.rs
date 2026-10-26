// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Per-Flow runtime: speculative overlay, proposals, RPC locks, and client operations.

mod client_operations;
mod commit_port;
mod flow_address;
mod flow_coordination_config;
mod flow_coordination_error;
mod flow_coordination_ports;
mod flow_coordinator;
pub(crate) mod read_operations;
mod registry;
mod rpc_locks;
mod speculative_overlay;

#[cfg(test)]
mod tests;

pub use commit_port::{
    CommitError, CommitTicket, CommitTicketSender, CommitUrgency, CommittedCommand,
    CommittedFlowCommand, FlowCommandCommitter, ReplayMismatch,
};
pub use flow_address::FlowAddress;
pub use flow_coordination_config::{FlowCoordinationConfig, default_run_settings};
pub use flow_coordination_error::{FlowCoordinationError, grpc_code};
pub use flow_coordination_ports::{
    FlowCoordinationEffectSink, HardDeleteReadError, HardDeleteUploadError,
    HardDeleteUploadOutcome, HardDeleteUploads, HardDeleteValueReader, HardDeleteValueUploader,
    RpcInvokeError, RpcWorkerInvoker, WorkerRpcInvocation, WorkerRpcResult,
};
pub use registry::{
    FlowCoordinatorDependencies, FlowCoordinatorRegistry, PendingInvocation, ProposalResult,
};
