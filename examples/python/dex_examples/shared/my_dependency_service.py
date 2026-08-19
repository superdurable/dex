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


class MyDependencyService:
    def update_external_system(self, message: str) -> None:
        print(
            "Update external system(like via RPC, or sending Kafka message or database):",
            message,
        )

    def send_email(self, recipient: str, subject: str, content: str) -> None:
        print(f"sending an email to {recipient}, title: {subject}, content: {content}")

    def charge_user(self, email: str, customer_id: str, amount: int) -> None:
        print(f"charge user customerID[{customer_id}] email[{email}] for ${amount}")

    def ship_item(self, order_id: str, test_fail_at_shipping: bool) -> None:
        if test_fail_at_shipping:
            raise RuntimeError(f"ship failed for order {order_id}")
        print(f"ship item {order_id}")

    def call_api1(self, data: str) -> None:
        print("call API1")

    def call_api2(self, data: str) -> None:
        print("call API2")

    def call_api3(self, data: str) -> None:
        print("call API3")

    def call_api4(self, data: str) -> None:
        print("call API4")

    def check_balance(self, account: str, amount: int) -> bool:
        return True

    def debit(self, account: str, amount: int) -> None:
        return None

    def credit(self, account: str, amount: int) -> None:
        return None

    def create_debit_memo(self, account: str, amount: int, notes: str) -> None:
        return None

    def create_credit_memo(self, account: str, amount: int, notes: str) -> None:
        return None

    def undo_debit(self, account: str, amount: int) -> None:
        return None

    def undo_credit(self, account: str, amount: int) -> None:
        return None

    def undo_create_debit_memo(self, account: str, amount: int, notes: str) -> None:
        return None

    def undo_create_credit_memo(self, account: str, amount: int, notes: str) -> None:
        return None
