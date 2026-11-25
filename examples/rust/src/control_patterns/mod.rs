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

//! Control-structure patterns: Deferred Choice, Critical Section, and Cancel
//! Case, each as one single-Flow example, and Child Flows with Business Identity.

pub mod cancel_case;
pub mod child_flows_business_identity;
pub mod critical_section;
pub mod deferred_choice;
pub mod fail_fast;

use std::sync::Arc;

use dex_sdk::{Client, Registry, SdkResult};

pub fn register(registry: Registry) -> SdkResult<Registry> {
    register_with_client(registry, None)
}

/// Registers the Flows for a Worker, whose loan application stops its credit
/// check child Flow through `client`.
pub fn register_worker(registry: Registry, client: Arc<Client>) -> SdkResult<Registry> {
    register_with_client(registry, Some(client))
}

fn register_with_client(registry: Registry, client: Option<Arc<Client>>) -> SdkResult<Registry> {
    let (loan_application, order_fulfillment, shipment) = match client {
        Some(client) => (
            cancel_case::LoanApplicationFlow::new(Arc::clone(&client)),
            child_flows_business_identity::OrderFulfillmentFlow::new(Arc::clone(&client)),
            child_flows_business_identity::ShipmentFlow::new(client),
        ),
        None => Default::default(),
    };
    registry
        .register(deferred_choice::OfferResponseFlow::default())?
        .register(critical_section::ContractEditFlow::default())?
        .register(cancel_case::CreditCheckFlow::default())?
        .register(loan_application)?
        .register(order_fulfillment)?
        .register(shipment)?
        .register(fail_fast::ExpenseReimbursementFlow::default())
}
