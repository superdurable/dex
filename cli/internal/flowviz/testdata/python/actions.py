# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from dataclasses import dataclass

import dex
from dex import Action, Attribute, AttributeMatch, Flow, PersistenceSchema, Step, StepList, force_complete, rpc, when_attribute_matches

CASE_STATUS = Attribute("case-status", str)
GATE_REQUEST_KEY = Attribute("gate-request-key", str)
APPROVE_PERMISSION = "refund.approve"


@dataclass
class RejectRefundInput:
    reason: str
    gate_request_key: str
    note: str | None = None


# dex:group group-id:review group-label:"Review"
# dex:explanation text:"Complete the Flow after the review."
class FinishStep(Step):
    def execute(self, context, step_input):
        return force_complete()


class RefundReviewFlow(Flow):
    def __init__(self):
        self.finish = FinishStep()

    def get_steps(self):
        return StepList.start_step(self.finish)

    def get_persistence_schema(self):
        return PersistenceSchema.of(CASE_STATUS, GATE_REQUEST_KEY)

    # dex:input field-name:reason value-type:string source:user required:true description:"Rejection reason"
    # dex:input field-name:gate_request_key value-type:string source:attribute attribute-key:gate-request-key required:true description:"Approval gate"
    # dex:input field-name:note value-type:string source:user required:false description:"Note"
    @rpc(
        lock_attributes=(CASE_STATUS.lock(),),
        action=Action(
            "Reject refund",
            required_permission=APPROVE_PERMISSION,
            condition=when_attribute_matches(CASE_STATUS, AttributeMatch.equal_to("awaiting-manager"), AttributeMatch.equal_to("escalated")),
        ),
    )
    def reject_refund(self, context, request: RejectRefundInput):
        CASE_STATUS.set(context, f"rejected: {request.reason}")

    @rpc(name="cancel_review", action=dex.Action("Cancel review", required_permission="refund.cancel"))
    def cancel_review_case(self, context):
        CASE_STATUS.set(context, "canceled")

    @rpc(name="describe_status")
    def describe_status(self, context) -> str:
        return CASE_STATUS.get(context)
