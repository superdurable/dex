// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Node-owned RPC calls shared by concurrent request IDs.

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};

use dexzd_keyspace::RunId;
use dexzd_protocol::dex::{ErrorSubStatus, InvokeRpcResponse};
use tokio::sync::watch;
use tokio_util::sync::CancellationToken;
use tokio_util::task::TaskTracker;
use tokio_util::task::task_tracker::TaskTrackerToken;

use crate::flow_coordination::{FlowAddress, FlowCoordinationError, grpc_code};

type RpcResult = Result<InvokeRpcResponse, FlowCoordinationError>;
type RpcRequestKey = (FlowAddress, RunId, String);
pub(crate) type RpcResultReceiver = watch::Receiver<Option<RpcResult>>;

pub(crate) struct RpcRequestTable {
    requests: Mutex<BTreeMap<RpcRequestKey, PendingRpcRequest>>,
    maximum_requests: usize,
    tasks: TaskTracker,
    pub(crate) stopped: CancellationToken,
}

struct PendingRpcRequest {
    fingerprint: Vec<u8>,
    result: RpcResultReceiver,
}

pub(crate) struct RpcRequestGuard {
    table: Arc<RpcRequestTable>,
    key: RpcRequestKey,
    sender: Option<watch::Sender<Option<RpcResult>>>,
    _task: TaskTrackerToken,
}

impl RpcRequestTable {
    pub(crate) fn new(maximum_requests: usize) -> Self {
        Self {
            requests: Mutex::new(BTreeMap::new()),
            maximum_requests,
            tasks: TaskTracker::new(),
            stopped: CancellationToken::new(),
        }
    }

    pub(crate) fn join_or_insert(
        self: &Arc<Self>,
        flow: &FlowAddress,
        run_id: RunId,
        request_id: &str,
        fingerprint: Vec<u8>,
    ) -> Result<(RpcResultReceiver, Option<RpcRequestGuard>), FlowCoordinationError> {
        let mut requests = self
            .requests
            .lock()
            .expect("RPC request table lock is not poisoned");
        let key = (flow.clone(), run_id, request_id.to_owned());
        if let Some(pending) = requests.get(&key) {
            if pending.fingerprint != fingerprint {
                return Err(FlowCoordinationError::invalid_argument(format!(
                    "request ID {request_id} was already used for a different request"
                )));
            }
            return Ok((pending.result.clone(), None));
        }
        if self.tasks.is_closed() {
            return Err(FlowCoordinationError::NodeStopping {
                detail: "node is draining RPC tasks".to_owned(),
            });
        }
        if requests.len() >= self.maximum_requests {
            return Err(FlowCoordinationError::rejected(
                grpc_code::RESOURCE_EXHAUSTED,
                ErrorSubStatus::Uncategorized,
                format!(
                    "node reached its {} in-flight RPC request limit",
                    self.maximum_requests
                ),
            ));
        }
        let (sender, receiver) = watch::channel(None);
        requests.insert(
            key.clone(),
            PendingRpcRequest {
                fingerprint,
                result: receiver.clone(),
            },
        );
        // Reserve task ownership under the admission lock so shutdown cannot miss a not-yet-spawned task.
        let guard = RpcRequestGuard {
            table: Arc::clone(self),
            key,
            sender: Some(sender),
            _task: self.tasks.token(),
        };
        Ok((receiver, Some(guard)))
    }

    pub(crate) fn close(&self) {
        let _requests = self
            .requests
            .lock()
            .expect("RPC request table lock is not poisoned");
        self.tasks.close();
    }

    pub(crate) fn stop(&self) {
        self.close();
        self.stopped.cancel();
    }

    pub(crate) async fn wait(&self) {
        self.tasks.wait().await;
    }
}

impl RpcRequestGuard {
    pub(crate) fn finish(mut self, result: RpcResult) {
        self.resolve(result);
    }

    fn resolve(&mut self, result: RpcResult) {
        if let Some(sender) = self.sender.take() {
            let mut requests = self
                .table
                .requests
                .lock()
                .expect("RPC request table lock is not poisoned");
            sender.send_replace(Some(result));
            requests.remove(&self.key);
        }
    }
}

impl Drop for RpcRequestGuard {
    fn drop(&mut self) {
        self.resolve(Err(FlowCoordinationError::Internal {
            detail: "RPC task ended without a result".to_owned(),
        }));
    }
}
