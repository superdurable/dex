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

//! AND-join and threads: Generalised AND-Join (WCP-33), Thread Merge
//! (WCP-41), and Thread Split (WCP-42).

use std::sync::LazyLock;

use dex_sdk::{
    Channel, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, Rpc, RpcList, Step,
    StepDecision, StepList, StepMovement, Wait,
};
use serde::{Deserialize, Serialize};

use super::sorted;

static INVOICE_RECEIVED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("invoice_received"));
static PAYMENT_RECEIVED: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("payment_received"));

pub const DELIVER_INVOICE: Rpc<String, ()> = Rpc::new("deliver_invoice");
pub const DELIVER_PAYMENT: Rpc<String, ()> = Rpc::new("deliver_payment");

/// The progress of the matching join.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct InvoicePaymentMatching {
    pub expected_matches: usize,
    pub matched_count: usize,
}

/// One invoice joined with one payment.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct InvoicePaymentMatch {
    pub invoice_id: String,
    pub payment_id: String,
}

/// WCP-33 Generalised AND-Join: the join fires once each Channel holds a
/// message, and extra messages stay queued for its next firing.
#[derive(Default)]
pub struct GeneralizedAndJoinFlow {
    match_invoice_and_payment: MatchInvoiceAndPayment,
    record_match: RecordMatch,
}

impl GeneralizedAndJoinFlow {
    fn deliver_invoice(&self, context: &mut Context, invoice_id: String) -> HandlerResult<()> {
        INVOICE_RECEIVED.publish(context, invoice_id)
    }

    fn deliver_payment(&self, context: &mut Context, payment_id: String) -> HandlerResult<()> {
        PAYMENT_RECEIVED.publish(context, payment_id)
    }
}

impl Flow for GeneralizedAndJoinFlow {
    type StartInput = InvoicePaymentMatching;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.match_invoice_and_payment).and(&self.record_match)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&INVOICE_RECEIVED)
            .channel(&PAYMENT_RECEIVED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure(DELIVER_INVOICE, Self::deliver_invoice)
            .procedure(DELIVER_PAYMENT, Self::deliver_payment)
    }
}

#[derive(Default)]
struct MatchInvoiceAndPayment;

impl Step for MatchInvoiceAndPayment {
    type Input = InvoicePaymentMatching;

    fn wait_for(&self, _context: &mut Context, _matching: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::all_of([
            INVOICE_RECEIVED.for_one(),
            PAYMENT_RECEIVED.for_one(),
        ]))
    }

    fn execute(&self, context: &mut Context, matching: Self::Input) -> HandlerResult<StepDecision> {
        let invoice_id = single_condition_result(context, &INVOICE_RECEIVED)?;
        let payment_id = single_condition_result(context, &PAYMENT_RECEIVED)?;
        let mut movements = vec![StepMovement::to(
            &RecordMatch,
            InvoicePaymentMatch {
                invoice_id,
                payment_id,
            },
        )];
        let matched_count = matching.matched_count + 1;
        if matched_count < matching.expected_matches {
            movements.push(StepMovement::to(
                &MatchInvoiceAndPayment,
                InvoicePaymentMatching {
                    expected_matches: matching.expected_matches,
                    matched_count,
                },
            ));
        }
        Ok(StepDecision::go_to_many(movements))
    }
}

#[derive(Default)]
pub struct RecordMatch;

impl Step for RecordMatch {
    type Input = InvoicePaymentMatch;

    fn execute(
        &self,
        _context: &mut Context,
        invoice_payment_match: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(invoice_payment_match))
    }
}

fn single_condition_result(context: &Context, channel: &Channel<String>) -> HandlerResult<String> {
    channel
        .condition_results(context)?
        .into_iter()
        .next()
        .ok_or_else(|| HandlerError::new("MissingChannelMessage", "the join consumed no message"))
}

static PARCEL_PACKED: LazyLock<Channel<usize>> = LazyLock::new(|| Channel::new("parcel_packed"));

/// WCP-42 Thread Split and WCP-41 Thread Merge: GoToMany starts several
/// executions of one Step type, and the merge waits for one message each.
#[derive(Default)]
pub struct ThreadSplitMergeFlow {
    split_shipment: SplitShipment,
    pack_parcel: PackParcel,
    merge_parcels: MergeParcels,
}

impl Flow for ThreadSplitMergeFlow {
    type StartInput = usize;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.split_shipment)
            .and(&self.pack_parcel)
            .and(&self.merge_parcels)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&PARCEL_PACKED)
    }
}

#[derive(Default)]
struct SplitShipment;

impl Step for SplitShipment {
    type Input = usize;

    fn execute(
        &self,
        _context: &mut Context,
        parcel_count: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let parcels = (0..parcel_count).map(|index| StepMovement::to(&PackParcel, index));
        Ok(StepDecision::go_to_many(
            parcels.chain([StepMovement::to(&MergeParcels, parcel_count)]),
        ))
    }
}

#[derive(Default)]
struct PackParcel;

impl Step for PackParcel {
    type Input = usize;

    fn execute(&self, context: &mut Context, index: Self::Input) -> HandlerResult<StepDecision> {
        PARCEL_PACKED.publish(context, index)?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct MergeParcels;

impl Step for MergeParcels {
    type Input = usize;

    fn wait_for(&self, _context: &mut Context, parcel_count: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(PARCEL_PACKED.for_n(parcel_count)))
    }

    fn execute(
        &self,
        context: &mut Context,
        _parcel_count: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let packed_parcels = PARCEL_PACKED.condition_results(context)?;
        Ok(StepDecision::graceful_complete(sorted(packed_parcels)))
    }
}
