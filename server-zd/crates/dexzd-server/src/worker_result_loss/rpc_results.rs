// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};

use dexzd_engine::{RpcInvokeError, RpcWorkerInvoker, WorkerRpcInvocation, WorkerRpcResult};
use futures::future::BoxFuture;

use super::ResultLossDecisions;

/// Wraps the Worker RPC transport so that a dropped RPC result arrives as a Worker disconnect,
/// which the caller sees as `UNAVAILABLE` with nothing committed.
pub(super) struct ResultLossRpcInvoker {
    inner: Arc<dyn RpcWorkerInvoker>,
    decisions: ResultLossDecisions,
    /// Worker RPC results seen so far per Flow ID and RPC name. An RPC has no attempt number, so
    /// this count tells a retried call from the first one. It lives as long as the server node.
    result_counts: Mutex<BTreeMap<(String, String), u64>>,
}

impl ResultLossRpcInvoker {
    pub(super) fn new(inner: Arc<dyn RpcWorkerInvoker>, decisions: ResultLossDecisions) -> Self {
        Self {
            inner,
            decisions,
            result_counts: Mutex::new(BTreeMap::new()),
        }
    }

    async fn invoke(
        &self,
        invocation: WorkerRpcInvocation,
    ) -> Result<WorkerRpcResult, RpcInvokeError> {
        // The invocation moves into the transport; the decision needs these two names afterwards.
        let flow_id = invocation.flow.flow_id.clone();
        let rpc_name = invocation.request.rpc_name.clone();
        let result = self.inner.invoke_worker_rpc(invocation).await?;
        let result_number = self.next_result_number(&flow_id, &rpc_name);
        let is_dropped = self.decisions.is_dropped(&[
            b"rpc",
            flow_id.as_bytes(),
            rpc_name.as_bytes(),
            &result_number.to_le_bytes(),
        ]);
        if !is_dropped {
            return Ok(result);
        }
        tracing::info!(
            %flow_id,
            %rpc_name,
            result_number,
            "development fault injection dropped an RPC result"
        );
        Err(RpcInvokeError::WorkerDisconnected {
            detail: self
                .decisions
                .dropped_result_detail("--inject-rpc-result-loss"),
        })
    }

    /// Returns the 1-based number of this Worker result among the results of the same Flow ID and
    /// RPC name.
    fn next_result_number(&self, flow_id: &str, rpc_name: &str) -> u64 {
        let mut counts = self
            .result_counts
            .lock()
            .expect("the RPC result counts lock is poisoned only if a holder panicked");
        let count = counts
            .entry((flow_id.to_owned(), rpc_name.to_owned()))
            .or_insert(0);
        *count += 1;
        *count
    }
}

impl RpcWorkerInvoker for ResultLossRpcInvoker {
    fn invoke_worker_rpc(
        &self,
        invocation: WorkerRpcInvocation,
    ) -> BoxFuture<'_, Result<WorkerRpcResult, RpcInvokeError>> {
        Box::pin(self.invoke(invocation))
    }
}
