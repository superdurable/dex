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

"""A user profile Flow whose selected Attributes project to PostgreSQL."""

from __future__ import annotations

from datetime import datetime

from dex import Attribute, Context, Flow, PersistenceSchema, RPCResult, StepList, rpc

from dex_examples.patterns.entity_store.user_profile import (
    UserProfile,
    UserProfileMetadata,
)

STORE_NAME = "entityStore"


class UserProfileFlow(Flow[None]):
    display_name = Attribute("display_name", str, sync_to_attribute_store=True)
    email = Attribute("email", str, sync_to_attribute_store=True)
    marketing_opt_in = Attribute("marketing_opt_in", bool, sync_to_attribute_store=True)
    credits = Attribute("credits", int, sync_to_attribute_store=True)
    weight = Attribute("weight", float, sync_to_attribute_store=True)
    last_logged_in_time = Attribute(
        "last_logged_in_time", datetime, sync_to_attribute_store=True
    )
    metadata = Attribute(
        "metadata", UserProfileMetadata, sync_to_attribute_store=True
    )

    def get_steps(self) -> StepList[None]:
        return StepList.empty()

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.display_name,
            self.email,
            self.marketing_opt_in,
            self.credits,
            self.weight,
            self.last_logged_in_time,
            self.metadata,
        )

    @rpc
    def update_profile(self, context: Context, input: UserProfile) -> None:
        input.validate()
        self.display_name.set(context, input.display_name)
        self.email.set(context, input.email)
        self.marketing_opt_in.set(context, input.marketing_opt_in)
        self.credits.set(context, input.credits)
        self.weight.set(context, input.weight)
        self.last_logged_in_time.set(context, input.last_logged_in_time)
        self.metadata.set(context, input.metadata)

    @rpc
    def get_profile(self, context: Context) -> RPCResult[UserProfile]:
        return RPCResult(
            UserProfile(
                self.display_name.get(context),
                self.email.get(context),
                self.marketing_opt_in.get(context),
                self.credits.get(context),
                self.weight.get(context),
                self.last_logged_in_time.get(context),
                self.metadata.get(context),
            )
        )

    @rpc
    def clear_profile(self, context: Context) -> None:
        self.display_name.delete(context)
        self.email.delete(context)
        self.marketing_opt_in.delete(context)
        self.credits.delete(context)
        self.weight.delete(context)
        self.last_logged_in_time.delete(context)
        self.metadata.delete(context)
