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
//! Runs the customer refund example: worker, start, decide, or list-refunds.

use std::error::Error;

use dex_examples_rust::products::customer_refund::billing_service::{
    DEFAULT_BILLING_LEDGER, FakeBillingService, format_usd,
};
use dex_examples_rust::products::customer_refund::email_service::DEFAULT_EMAIL_LEDGER;
use dex_examples_rust::products::customer_refund::refund_config::{
    BillingMode, RefundConfig, open_refund_blob_cache,
};
use dex_examples_rust::products::customer_refund::refund_flow::{
    APPROVE_REFUND, CustomerRefundFlow, DecisionReason, REJECT_REFUND,
};
use dex_examples_rust::products::customer_refund::refund_scenarios::RefundScenario;
use dex_examples_rust::products::customer_refund::refund_worker::{
    RunningRefundWorker, new_refund_flow, open_refund_client, refund_worker_options,
};
use dex_sdk::Client;

const USAGE: &str = "usage: customer-refund worker
       customer-refund start FLOW_ID [normal|fail-then-succeed|deny|small]
       customer-refund decide FLOW_ID approve|reject REASON
       customer-refund list-refunds";

fn main() -> Result<(), Box<dyn Error>> {
    let config = RefundConfig::from_environment()?;
    let arguments: Vec<String> = std::env::args().skip(1).collect();
    let arguments: Vec<&str> = arguments.iter().map(String::as_str).collect();
    match arguments.as_slice() {
        ["worker"] => run_worker(&config),
        ["start", flow_id] => start_refund(&config, flow_id, RefundScenario::Normal),
        ["start", flow_id, scenario] => start_refund(&config, flow_id, scenario.parse()?),
        ["decide", flow_id, "approve", reason] => decide_refund(&config, flow_id, true, reason),
        ["decide", flow_id, "reject", reason] => decide_refund(&config, flow_id, false, reason),
        ["list-refunds"] => list_refunds(),
        _ => Err(USAGE.into()),
    }
}

fn configured_refund_flow(config: &RefundConfig) -> CustomerRefundFlow {
    new_refund_flow(
        config.billing_mode,
        DEFAULT_BILLING_LEDGER,
        DEFAULT_EMAIL_LEDGER,
    )
}

fn run_worker(config: &RefundConfig) -> Result<(), Box<dyn Error>> {
    let cache = open_refund_blob_cache(".dex-blob-cache/customer-refund-worker")?;
    let options = refund_worker_options(&config.server_address);
    let worker = RunningRefundWorker::start(configured_refund_flow(config), cache, options)?;
    println!(
        "Worker is ready (billing mode {}). Press Ctrl+C to stop it.",
        config.billing_mode
    );
    tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()?
        .block_on(tokio::signal::ctrl_c())?;
    println!("Stopping the Worker.");
    worker.stop()
}

fn open_client(config: &RefundConfig) -> Result<(CustomerRefundFlow, Client), Box<dyn Error>> {
    let refund_flow = configured_refund_flow(config);
    let cache = open_refund_blob_cache(".dex-blob-cache/customer-refund-client")?;
    let client = open_refund_client(refund_flow.clone(), cache, &config.server_address)?;
    Ok((refund_flow, client))
}

fn start_refund(
    config: &RefundConfig,
    flow_id: &str,
    scenario: RefundScenario,
) -> Result<(), Box<dyn Error>> {
    let (refund_flow, client) = open_client(config)?;
    client.start_flow(&refund_flow, flow_id, scenario.refund_request())?;
    println!("Started refund {flow_id} ({scenario}).");
    Ok(())
}

fn decide_refund(
    config: &RefundConfig,
    flow_id: &str,
    approved: bool,
    reason: &str,
) -> Result<(), Box<dyn Error>> {
    let (_, client) = open_client(config)?;
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
    let refunds =
        FakeBillingService::new(DEFAULT_BILLING_LEDGER, BillingMode::Normal).list_refunds()?;
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
