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

use std::path::Path;

use dex_examples_rust::products::customer_refund::billing_service::FakeBillingService;
use dex_examples_rust::products::customer_refund::refund_config::BillingMode;
use dex_examples_rust::products::customer_refund::refund_flow::CustomerRefundFlow;
use dex_examples_rust::products::customer_refund::refund_worker::new_refund_flow;
use dex_examples_rust::shared::PULL_REGISTRATION_TIMEOUT;
use dex_sdk::Worker;

const REFUND_BILLING_LEDGER_FILE: &str = "billing-refunds.json";
const REFUND_EMAIL_LEDGER_FILE: &str = "sent-emails.json";

/// Returns the refund Flow and a reader of its billing ledger, both in a per-test directory.
pub(crate) fn new_temporary_refund_flow(
    ledger_directory: &Path,
) -> (CustomerRefundFlow, FakeBillingService) {
    let billing_ledger = ledger_directory.join(REFUND_BILLING_LEDGER_FILE);
    let flow = new_refund_flow(
        BillingMode::Normal,
        &billing_ledger,
        ledger_directory.join(REFUND_EMAIL_LEDGER_FILE),
    );
    (
        flow,
        FakeBillingService::new(billing_ledger, BillingMode::Normal),
    )
}

pub(crate) fn await_pull_registration(worker: &Worker) {
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("create pull registration runtime");
    runtime
        .block_on(async {
            tokio::time::timeout(PULL_REGISTRATION_TIMEOUT, worker.wait_pull_registered()).await
        })
        .expect("pull Worker registered before the timeout")
        .expect("pull Worker registration");
}
