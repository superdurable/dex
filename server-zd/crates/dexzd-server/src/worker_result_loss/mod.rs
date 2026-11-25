// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Development-only Worker result loss: drops seeded Worker results at the transport, like a Worker
//! crash after its handler ran.

mod rpc_results;
mod step_results;

use std::sync::Arc;

use dexzd_engine::{MethodDispatcher, RpcWorkerInvoker};
use xxhash_rust::xxh3::xxh3_64_with_seed;

use crate::config::{ResultLossShare, WorkerResultLossConfig};
use rpc_results::ResultLossRpcInvoker;
use step_results::ResultLossMethodDispatcher;

/// Returns `dispatcher`, wrapped to drop Step method results when `config` asks for it.
pub(crate) fn method_dispatcher(
    dispatcher: Arc<dyn MethodDispatcher>,
    config: WorkerResultLossConfig,
) -> Arc<dyn MethodDispatcher> {
    if config.step_results.is_none() {
        return dispatcher;
    }
    let decisions = ResultLossDecisions {
        share: config.step_results,
        seed: config.seed,
    };
    Arc::new(ResultLossMethodDispatcher::new(dispatcher, decisions))
}

/// Returns `invoker`, wrapped to drop RPC results when `config` asks for it.
pub(crate) fn rpc_invoker(
    invoker: Arc<dyn RpcWorkerInvoker>,
    config: WorkerResultLossConfig,
) -> Arc<dyn RpcWorkerInvoker> {
    if config.rpc_results.is_none() {
        return invoker;
    }
    let decisions = ResultLossDecisions {
        share: config.rpc_results,
        seed: config.seed,
    };
    Arc::new(ResultLossRpcInvoker::new(invoker, decisions))
}

/// Logs one startup warning when `config` drops Worker results, so injected retries are not
/// mistaken for real failures.
pub(crate) fn warn_when_enabled(config: WorkerResultLossConfig) {
    if !config.is_enabled() {
        return;
    }
    tracing::warn!(
        step_result_loss = config.step_results.fraction(),
        rpc_result_loss = config.rpc_results.fraction(),
        seed = config.seed,
        "development fault injection drops Worker results; dropped results fail like a Worker disconnect"
    );
}

/// Drops a Worker result by hashing the seed with the result's identity, never timing or
/// concurrency.
#[derive(Clone, Copy, Debug)]
struct ResultLossDecisions {
    share: ResultLossShare,
    seed: u64,
}

impl ResultLossDecisions {
    /// Returns true when the result identified by `identity_fields` is dropped.
    fn is_dropped(self, identity_fields: &[&[u8]]) -> bool {
        // Length prefixes keep ("ab", "c") and ("a", "bc") distinct.
        let mut identity = Vec::new();
        for field in identity_fields {
            identity.extend_from_slice(&(field.len() as u64).to_le_bytes());
            identity.extend_from_slice(field);
        }
        self.share
            .is_draw_dropped(xxh3_64_with_seed(&identity, self.seed))
    }

    /// Returns the Worker disconnect detail of a dropped result.
    fn dropped_result_detail(self, flag: &str) -> String {
        format!(
            "dev fault injection dropped the Worker result ({flag} {}, seed {})",
            self.share.fraction(),
            self.seed
        )
    }
}
