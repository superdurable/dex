# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from dataclasses import dataclass

from dex import (
    Action,
    Attribute,
    AttributeMatch,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    graceful_complete,
    rpc,
    when_attribute_matches,
)

CASE_STATUS = Attribute("case-status", str)
GATE_REQUEST_KEY = Attribute("gate-request-key", str)
PRIORITY = Attribute("priority", int)


@dataclass(frozen=True)
class RejectRefundInput:
    reason: str
    gate_request_key: str
    note: str | None = None


# dex:group group-id:hold group-label:"Hold"
# dex:explanation text:"Wait for the review to close."
class HoldStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete()


class RefundReviewFlow(Flow[None]):
    def __init__(self) -> None:
        self.hold = HoldStep()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.hold)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(CASE_STATUS, GATE_REQUEST_KEY, PRIORITY)

    # dex:input field-name:reason value-type:string source:user required:true description:"Rejection reason"
    # dex:input field-name:gate_request_key value-type:string source:attribute attribute-key:gate-request-key required:true description:"Approval gate"
    # dex:input field-name:note value-type:string source:user required:false description:"Note"
    @rpc(
        lock_attributes=(CASE_STATUS.lock(),),
        action=Action(
            "Reject refund",
            required_permission="refund.approve",
            condition=when_attribute_matches(
                CASE_STATUS,
                AttributeMatch.equal_to("awaiting-manager"),
                AttributeMatch.equal_to("escalated"),
            ),
        ),
    )
    def reject_refund(self, context: Context, input: RejectRefundInput) -> None:
        if CASE_STATUS.get(context) == "awaiting-manager":
            CASE_STATUS.set(context, f"rejected: {input.reason}")

    @rpc(
        action=Action(
            "Escalate",
            required_permission="refund.escalate",
            condition=when_attribute_matches(PRIORITY, AttributeMatch.equal_to(1), AttributeMatch.equal_to(2)),
        ),
    )
    def escalate_case(self, context: Context) -> None:
        if PRIORITY.get(context):
            CASE_STATUS.set(context, "escalated")
