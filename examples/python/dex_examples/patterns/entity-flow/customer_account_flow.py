# Copyright (c) 2022-2026 Super Durable, Inc.
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

from __future__ import annotations

from dataclasses import dataclass

from dex import (
    Attribute,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    graceful_complete,
    rpc,
)


def customer_account_flow_id(account_id: str) -> str:
    return f"customer-account-{account_id}"


@dataclass
class ContactDetails:
    display_name: str
    email: str

    def validate(self) -> None:
        if not self.display_name.strip():
            raise ValueError("display name is required")
        if "@" not in self.email:
            raise ValueError("email must contain @")


@dataclass
class CreditAdjustment:
    amount: int


# dex:group group-id:account-retirement group-label:"Account retirement"
# dex:explanation text:"Complete the account Flow after the retire request passes its checks."
class CloseAccountStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete()


class CustomerAccountFlow(Flow[None]):
    display_name = Attribute("display_name", str)
    email = Attribute("email", str)
    credit_balance = Attribute("credit_balance", int)
    retired = Attribute("retired", bool)

    def get_steps(self) -> StepList[None]:
        return StepList.empty().other_steps(CloseAccountStep())

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.display_name, self.email, self.credit_balance, self.retired
        )

    @rpc(name="UpdateContactDetails")
    def update_contact_details(self, context: Context, input: ContactDetails) -> None:
        input.validate()
        self.display_name.set(context, input.display_name)
        self.email.set(context, input.email)

    @rpc(name="AdjustCreditBalance", lock_attributes=(credit_balance.lock(),))
    def adjust_credit_balance(
        self, context: Context, input: CreditAdjustment
    ) -> RPCResult[int]:
        if self.retired.get(context):
            raise ValueError("account is retired")
        if input.amount == 0:
            raise ValueError("amount must not be zero")
        balance = self.credit_balance.get(context) + input.amount
        if balance < 0:
            raise ValueError("credit balance cannot go below zero")
        self.credit_balance.set(context, balance)
        return RPCResult(balance)

    @rpc(name="RetireAccount", lock_attributes=(credit_balance.lock(),))
    def retire_account(self, context: Context) -> RPCResult[None]:
        if self.credit_balance.get(context) != 0:
            raise ValueError("spend or refund the credit balance before retiring")
        self.retired.set(context, True)
        return RPCResult(None, next_steps=(StepMovement.of(CloseAccountStep, None),))
