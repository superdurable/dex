# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Workflow Patterns WCP-33 (generalised AND-join), WCP-41, and WCP-42 (threads)."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Callable

from dex import (
    Channel,
    Context,
    Flow,
    Join,
    JoinRound,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Wait,
    go_to_many,
    graceful_complete,
    rpc,
)

# WCP-33 Generalised AND-Join: AllOf fires once per pair; extra messages wait.


@dataclass(frozen=True)
class InvoicePaymentMatching:
    expected_matches: int
    matched_count: int


@dataclass(frozen=True)
class InvoicePaymentMatch:
    invoice_id: str
    payment_id: str


INVOICE_RECEIVED = Channel("invoiceReceived", str)
PAYMENT_RECEIVED = Channel("paymentReceived", str)


class MatchInvoiceAndPayment(Step[InvoicePaymentMatching]):
    def wait_for(self, context: Context, input: InvoicePaymentMatching) -> Wait:
        return Wait.all_of(INVOICE_RECEIVED.for_one(), PAYMENT_RECEIVED.for_one())

    def execute(self, context: Context, input: InvoicePaymentMatching) -> StepDecision:
        match = InvoicePaymentMatch(
            INVOICE_RECEIVED.results(context)[0], PAYMENT_RECEIVED.results(context)[0]
        )
        matched_count = input.matched_count + 1
        movements: list[StepMovement[Any]] = [StepMovement.of(RecordMatch, match)]
        if matched_count < input.expected_matches:
            movements.append(
                StepMovement.of(
                    MatchInvoiceAndPayment,
                    InvoicePaymentMatching(input.expected_matches, matched_count),
                )
            )
        return go_to_many(*movements)


class RecordMatch(Step[InvoicePaymentMatch]):
    def execute(self, context: Context, input: InvoicePaymentMatch) -> StepDecision:
        return graceful_complete(input)


class GeneralizedAndJoinFlow(Flow[InvoicePaymentMatching]):
    def __init__(self) -> None:
        self.match_invoice_and_payment = MatchInvoiceAndPayment()
        self.record_match = RecordMatch()

    def get_steps(self) -> StepList[InvoicePaymentMatching]:
        return StepList.start_step(self.match_invoice_and_payment).other_steps(
            self.record_match
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(INVOICE_RECEIVED, PAYMENT_RECEIVED)

    @rpc
    def deliver_invoice(self, context: Context, input: str) -> None:
        INVOICE_RECEIVED.publish(context, input)

    @rpc
    def deliver_payment(self, context: Context, input: str) -> None:
        PAYMENT_RECEIVED.publish(context, input)


# WCP-42 Thread Split and WCP-41 Thread Merge: threads of one Step type, then a Join.

PARCEL_PACKED = Join("parcelPacked", int)


@dataclass(frozen=True)
class ParcelPacking:
    parcel_index: int
    join_round: JoinRound


def build_pack_parcel(parcel_index: int) -> Callable[[JoinRound], StepMovement[ParcelPacking]]:
    return lambda join_round: StepMovement.of(
        PackParcel, ParcelPacking(parcel_index, join_round)
    )


class SplitShipment(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        return go_to_many(
            *PARCEL_PACKED.fork(
                context,
                [build_pack_parcel(parcel_index) for parcel_index in range(input)],
                lambda join_round: StepMovement.of(MergeParcels, join_round),
            )
        )


class PackParcel(Step[ParcelPacking]):
    def execute(self, context: Context, input: ParcelPacking) -> StepDecision:
        return PARCEL_PACKED.end_branch(context, input.join_round, input.parcel_index)


class MergeParcels(Step[JoinRound]):
    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.until(PARCEL_PACKED.for_all_branches(input))

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        return graceful_complete(sorted(PARCEL_PACKED.branch_results(context, input)))


class ThreadSplitMergeFlow(Flow[int]):
    def __init__(self) -> None:
        self.split_shipment = SplitShipment()
        self.pack_parcel = PackParcel()
        self.merge_parcels = MergeParcels()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.split_shipment).other_steps(
            self.pack_parcel, self.merge_parcels
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(PARCEL_PACKED)
