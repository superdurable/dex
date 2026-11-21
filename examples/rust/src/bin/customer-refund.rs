// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//! Runnable Rust examples for Dex Flows, Steps, and application patterns.
//!
//! The customer refund commands.

use std::error::Error;
use std::time::Duration;

use dex_examples_rust::products::customer_refund::billing_service::{
    DEFAULT_BILLING_LEDGER, format_usd, list_recorded_refunds,
};
use dex_examples_rust::products::customer_refund::email_service::DEFAULT_EMAIL_LEDGER;
use dex_examples_rust::products::customer_refund::refund_config::{
    BillingMode, RefundConfig, RefundWorkerConfig, open_refund_blob_cache,
};
use dex_examples_rust::products::customer_refund::refund_flow::{
    APPROVE_REFUND, CustomerRefundFlow, DecisionReason, REJECT_REFUND,
};
use dex_examples_rust::products::customer_refund::refund_scenarios::RefundScenario;
use dex_examples_rust::products::customer_refund::refund_worker::{
    RunningRefundWorker, new_refund_flow, open_refund_client, refund_worker_options,
};
use dex_sdk::Client;
use tracing_subscriber::filter::LevelFilter;

const USAGE: &str = "usage: customer-refund worker
       customer-refund start FLOW_ID [normal|fail-then-succeed|deny|small]
       customer-refund decide FLOW_ID approve|reject REASON
       customer-refund list-refunds";

fn main() -> Result<(), Box<dyn Error>> {
    let arguments: Vec<String> = std::env::args().skip(1).collect();
    let arguments: Vec<&str> = arguments.iter().map(String::as_str).collect();
    match arguments.as_slice() {
        ["worker"] => run_worker(&RefundWorkerConfig::from_environment()?),
        ["start", flow_id] => start_refund(flow_id, RefundScenario::Normal),
        ["start", flow_id, scenario] => start_refund(flow_id, scenario.parse()?),
        ["decide", flow_id, "approve", reason] => decide_refund(flow_id, true, reason),
        ["decide", flow_id, "reject", reason] => decide_refund(flow_id, false, reason),
        ["list-refunds"] => list_refunds(),
        _ => Err(USAGE.into()),
    }
}

fn run_worker(config: &RefundWorkerConfig) -> Result<(), Box<dyn Error>> {
    tracing_subscriber::fmt()
        .with_max_level(LevelFilter::WARN)
        .try_init()
        .map_err(|error| format!("install the log subscriber: {error}"))?;
    let refund_flow = new_refund_flow(
        config.billing_mode,
        DEFAULT_BILLING_LEDGER,
        DEFAULT_EMAIL_LEDGER,
    );
    let cache = open_refund_blob_cache(".dex-blob-cache/customer-refund-worker")?;
    let options = refund_worker_options(&config.refund.server_address);
    let worker = RunningRefundWorker::start(refund_flow, cache, options)?;
    println!(
        "Worker is ready (billing mode {}). Press Ctrl+C to stop it.",
        config.billing_mode
    );
    tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()?
        .block_on(wait_for_interrupt_or_worker_end(&worker))?;
    println!("Stopping the Worker.");
    worker.stop()
}

async fn wait_for_interrupt_or_worker_end(worker: &RunningRefundWorker) -> std::io::Result<()> {
    let interrupt = tokio::signal::ctrl_c();
    tokio::pin!(interrupt);
    let mut worker_check = tokio::time::interval(Duration::from_millis(500));
    loop {
        tokio::select! {
            interrupted = &mut interrupt => return interrupted,
            _ = worker_check.tick() => {
                if worker.has_stopped() {
                    return Ok(());
                }
            }
        }
    }
}

/// Opens a Client; it registers the Flow in the normal billing mode because it runs no Steps.
fn open_client() -> Result<(CustomerRefundFlow, Client), Box<dyn Error>> {
    let config = RefundConfig::from_environment();
    let refund_flow = new_refund_flow(
        BillingMode::Normal,
        DEFAULT_BILLING_LEDGER,
        DEFAULT_EMAIL_LEDGER,
    );
    let cache = open_refund_blob_cache(".dex-blob-cache/customer-refund-client")?;
    let client = open_refund_client(refund_flow.clone(), cache, &config.server_address)?;
    Ok((refund_flow, client))
}

fn start_refund(flow_id: &str, scenario: RefundScenario) -> Result<(), Box<dyn Error>> {
    let (refund_flow, client) = open_client()?;
    client.start_flow(&refund_flow, flow_id, scenario.refund_request())?;
    println!("Started refund {flow_id} ({scenario}).");
    Ok(())
}

fn decide_refund(flow_id: &str, approved: bool, reason: &str) -> Result<(), Box<dyn Error>> {
    let (_, client) = open_client()?;
    let rpc = if approved {
        APPROVE_REFUND
    } else {
        REJECT_REFUND
    };
    let decision = DecisionReason {
        reason: reason.to_string(),
    };
    client.invoke_rpc(flow_id, rpc, decision)?;
    let outcome = if approved { "approved" } else { "rejected" };
    println!("Refund {flow_id}: {outcome}.");
    Ok(())
}

fn list_refunds() -> Result<(), Box<dyn Error>> {
    let refunds = list_recorded_refunds(DEFAULT_BILLING_LEDGER)?;
    if refunds.is_empty() {
        println!("No refunds recorded.");
    }
    for refund in refunds {
        println!(
            "{}  {}  {} USD  key {}",
            refund.refund_id,
            refund.order_id,
            format_usd(refund.amount_cents),
            refund.idempotency_key
        );
    }
    Ok(())
}
