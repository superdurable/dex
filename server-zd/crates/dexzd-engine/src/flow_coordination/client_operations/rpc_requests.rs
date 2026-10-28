// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Node-owned RPC calls shared by concurrent request IDs.

use std::collections::BTreeMap;
use std::sync::Mutex;

use dexzd_keyspace::RunId;
use dexzd_protocol::dex::InvokeRpcResponse;
use tokio::sync::watch;
use tokio_util::sync::CancellationToken;
use tokio_util::task::TaskTracker;

use crate::flow_coordination::{FlowAddress, FlowCoordinationError};

type RpcResult = Result<InvokeRpcResponse, FlowCoordinationError>;
type RpcRequestKey = (FlowAddress, RunId, String);
pub(crate) type RpcResultReceiver = watch::Receiver<Option<RpcResult>>;
pub(crate) type RpcResultSender = watch::Sender<Option<RpcResult>>;

#[derive(Default)]
pub(crate) struct RpcRequestTable {
    requests: Mutex<BTreeMap<RpcRequestKey, PendingRpcRequest>>,
    pub(crate) tasks: TaskTracker,
    pub(crate) stopped: CancellationToken,
}

struct PendingRpcRequest {
    fingerprint: Vec<u8>,
    result: RpcResultReceiver,
}

impl RpcRequestTable {
    pub(crate) fn join_or_insert(
        &self,
        flow: &FlowAddress,
        run_id: RunId,
        request_id: &str,
        fingerprint: Vec<u8>,
    ) -> Result<(RpcResultReceiver, Option<RpcResultSender>), FlowCoordinationError> {
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
        let (sender, receiver) = watch::channel(None);
        requests.insert(
            key,
            PendingRpcRequest {
                fingerprint,
                result: receiver.clone(),
            },
        );
        Ok((receiver, Some(sender)))
    }

    pub(crate) fn finish(
        &self,
        flow: FlowAddress,
        run_id: RunId,
        request_id: String,
        sender: RpcResultSender,
        result: RpcResult,
    ) {
        let mut requests = self
            .requests
            .lock()
            .expect("RPC request table lock is not poisoned");
        sender.send_replace(Some(result));
        requests.remove(&(flow, run_id, request_id));
    }
}
