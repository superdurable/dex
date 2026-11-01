// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! AdminService describe and step down calls, answered by the process lifecycle.

use std::time::Duration;

use async_trait::async_trait;
use dexzd_api::{
    ActiveNodeAdministration, ActiveNodeDescription, ActiveNodeDescriptionError,
    ActiveNodeStepDownError, ActiveNodeStepDownOutcome, LeaseHolderAfterStepDown, NodeLeaseHolder,
    StepDownPrecondition,
};
use dexzd_protocol::dexzd::v1::NodeLease;
use tokio::sync::{mpsc, oneshot};

/// Calls that wait for the lifecycle; later calls wait in AdminService.
const REQUEST_CAPACITY: usize = 8;

type DescriptionReply = Result<ActiveNodeDescription, ActiveNodeDescriptionError>;
type StepDownReply = Result<ActiveNodeStepDownOutcome, ActiveNodeStepDownError>;

/// One AdminService call; the lifecycle answers it even after the caller stops waiting.
pub(in crate::server_node) enum AdministrationRequest {
    Describe {
        reply: oneshot::Sender<DescriptionReply>,
    },
    StepDown(StepDownRequest),
}

/// A step down call and the node lease it expects the active process to hold.
pub(in crate::server_node) struct StepDownRequest {
    precondition: StepDownPrecondition,
    reply: oneshot::Sender<StepDownReply>,
    /// The call came through a standby-phase gRPC instance, whose connections close within seconds.
    is_on_standby_instance: bool,
}

/// The node lease that the serving process holds, for describe and step down answers.
#[derive(Clone, Debug)]
pub(in crate::server_node) struct HeldNodeLease {
    pub(in crate::server_node) node_id: String,
    pub(in crate::server_node) node_epoch: u64,
    pub(in crate::server_node) holder_process_id: String,
}

impl AdministrationRequest {
    /// Answers that this process holds no lease, because it is `state`.
    pub(in crate::server_node) fn refuse_not_active(self, state: &str) {
        match self {
            Self::Describe { reply } => send(
                reply,
                Err(ActiveNodeDescriptionError::NotActive {
                    detail: state.to_owned(),
                }),
            ),
            Self::StepDown(request) => request.refuse_not_active(state),
        }
    }

    /// Answers a describe call; returns a step down call for the caller to handle.
    pub(in crate::server_node) fn answer_description(
        self,
        lease: &HeldNodeLease,
    ) -> Option<StepDownRequest> {
        match self {
            Self::Describe { reply } => {
                send(
                    reply,
                    Ok(ActiveNodeDescription {
                        node_id: lease.node_id.clone(),
                        node_epoch: lease.node_epoch,
                        holder_process_id: lease.holder_process_id.clone(),
                    }),
                );
                None
            }
            Self::StepDown(request) => Some(request),
        }
    }
}

impl StepDownRequest {
    /// Returns whether the call came through a standby-phase instance, which closes its connection
    /// before a step down could answer.
    pub(in crate::server_node) fn is_on_standby_instance(&self) -> bool {
        self.is_on_standby_instance
    }

    pub(in crate::server_node) fn refuse_not_active(self, state: &str) {
        send(
            self.reply,
            Err(ActiveNodeStepDownError::NotActive {
                detail: state.to_owned(),
            }),
        );
    }

    pub(in crate::server_node) fn refuse_already_stepping_down(self) {
        send(
            self.reply,
            Err(ActiveNodeStepDownError::AlreadySteppingDown),
        );
    }

    /// Refuses when `held` does not satisfy the precondition; otherwise returns the request.
    pub(in crate::server_node) fn check_precondition(self, held: &HeldNodeLease) -> Option<Self> {
        if self
            .precondition
            .is_satisfied_by(&held.node_id, held.node_epoch)
        {
            return Some(self);
        }
        send(
            self.reply,
            Err(ActiveNodeStepDownError::PreconditionMismatch {
                expected: self.precondition,
                active_node_id: held.node_id.clone(),
                active_node_epoch: held.node_epoch,
            }),
        );
        None
    }

    /// Answers that the node stopped instead of stepping down; the process then exits.
    pub(in crate::server_node) fn report_internal_failure(self, detail: String) {
        send(
            self.reply,
            Err(ActiveNodeStepDownError::Internal { detail }),
        );
    }

    pub(in crate::server_node) fn report_unconfirmed_release(self, detail: String) {
        send(
            self.reply,
            Err(ActiveNodeStepDownError::LeaseReleaseUnconfirmed { detail }),
        );
    }
}

fn send<T>(reply: oneshot::Sender<T>, answer: T) {
    // A caller that stopped waiting still had its call applied.
    if reply.send(answer).is_err() {
        tracing::info!("an AdminService caller stopped waiting before the answer");
    }
}

/// The AdminService administration, forwarded to the process lifecycle.
pub(in crate::server_node) struct LifecycleActiveNodeAdministration {
    requests: mpsc::Sender<AdministrationRequest>,
    /// Marks step downs from a standby-phase instance, so a serving lifecycle refuses them at once.
    is_standby_instance: bool,
}

impl LifecycleActiveNodeAdministration {
    /// Returns the administration of serving instances and the receiver that the lifecycle answers.
    pub(in crate::server_node) fn new() -> (Self, mpsc::Receiver<AdministrationRequest>) {
        let (requests, receiver) = mpsc::channel(REQUEST_CAPACITY);
        (
            Self {
                requests,
                is_standby_instance: false,
            },
            receiver,
        )
    }

    /// Returns the administration of standby-phase instances, answered by the same lifecycle.
    pub(in crate::server_node) fn for_standby_instances(&self) -> Self {
        Self {
            requests: self.requests.clone(),
            is_standby_instance: true,
        }
    }
}

#[async_trait]
impl ActiveNodeAdministration for LifecycleActiveNodeAdministration {
    async fn describe_active_node(&self) -> DescriptionReply {
        let (reply, answer) = oneshot::channel();
        if self
            .requests
            .send(AdministrationRequest::Describe { reply })
            .await
            .is_err()
        {
            return Err(ActiveNodeDescriptionError::NotActive {
                detail: "the server process is stopping".to_owned(),
            });
        }
        answer.await.unwrap_or_else(|_| {
            Err(ActiveNodeDescriptionError::NotActive {
                detail: "the server process stopped before it answered".to_owned(),
            })
        })
    }

    async fn step_down_active_node(&self, precondition: StepDownPrecondition) -> StepDownReply {
        let (reply, answer) = oneshot::channel();
        self.requests
            .send(AdministrationRequest::StepDown(StepDownRequest {
                precondition,
                reply,
                is_on_standby_instance: self.is_standby_instance,
            }))
            .await
            .map_err(|_| ActiveNodeStepDownError::NotActive {
                detail: "the server process is stopping".to_owned(),
            })?;
        answer
            .await
            .map_err(|_| ActiveNodeStepDownError::Internal {
                detail: "the server process stopped before it answered".to_owned(),
            })?
    }
}

/// A released lease whose step down answer waits to learn who holds the lease next.
pub(in crate::server_node) struct PendingStepDownAnswer {
    reply: oneshot::Sender<StepDownReply>,
    node_id: String,
    released_node_epoch: u64,
    hold_off: Duration,
    /// Dropped with the answer, which lets the old gRPC instance send GOAWAY.
    _answered: oneshot::Sender<()>,
}

impl PendingStepDownAnswer {
    pub(in crate::server_node) fn new(
        request: StepDownRequest,
        released: &HeldNodeLease,
        hold_off: Duration,
        answered: oneshot::Sender<()>,
    ) -> Self {
        Self {
            reply: request.reply,
            node_id: released.node_id.clone(),
            released_node_epoch: released.node_epoch,
            hold_off,
            _answered: answered,
        }
    }

    /// Answers when `lease` shows another holder's new epoch; otherwise returns `self` to wait more.
    pub(in crate::server_node) fn answer_on_takeover(
        self,
        lease: Option<&NodeLease>,
        own_holder_process_id: &str,
    ) -> Option<Self> {
        match lease {
            Some(lease)
                if lease.epoch > self.released_node_epoch
                    && lease.holder_process_id != own_holder_process_id =>
            {
                let holder = NodeLeaseHolder {
                    node_epoch: lease.epoch,
                    holder_process_id: lease.holder_process_id.clone(),
                };
                self.answer(LeaseHolderAfterStepDown::TakenOverByAnotherProcess(holder));
                None
            }
            _ => Some(self),
        }
    }

    /// Answers that this process took the lease back after the hold-off.
    pub(in crate::server_node) fn answer_taken_back(self, lease: &HeldNodeLease) {
        self.answer(LeaseHolderAfterStepDown::TakenBackByThisProcess(
            NodeLeaseHolder {
                node_epoch: lease.node_epoch,
                holder_process_id: lease.holder_process_id.clone(),
            },
        ));
    }

    /// Answers that neither a takeover nor a take-back was confirmed, because of `detail`.
    pub(in crate::server_node) fn answer_unknown(self, detail: String) {
        self.answer(LeaseHolderAfterStepDown::Unknown { detail });
    }

    /// Answers that the process stops after a failure, as after losing its lease.
    pub(in crate::server_node) fn answer_failure(self, detail: String) {
        send(
            self.reply,
            Err(ActiveNodeStepDownError::Internal { detail }),
        );
    }

    fn answer(self, lease_holder_after_step_down: LeaseHolderAfterStepDown) {
        send(
            self.reply,
            Ok(ActiveNodeStepDownOutcome {
                node_id: self.node_id,
                released_node_epoch: self.released_node_epoch,
                lease_reacquisition_hold_off: self.hold_off,
                lease_holder_after_step_down,
            }),
        );
    }
}
