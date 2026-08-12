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

/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

use dex_sdk::{
    Attribute, Context, Flow, FlowConfig, HandlerResult, PersistenceSchema, Rpc, RpcList,
    RpcResult, Step, StepDecision, StepList,
};
use serde::{Deserialize, Serialize};

pub const USER_PROFILE_READ: Rpc<(), UserProfile> = Rpc::new("UserProfileRead");
pub const USER_PROFILE_UPDATE: Rpc<UserProfile, UserProfile> = Rpc::new("UserProfileUpdate");

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
pub struct UserProfile {
    pub name: String,
    pub email: String,
}

#[derive(Default)]
pub struct UserProfileFlow {
    create: Create,
}

impl UserProfileFlow {
    pub fn flow_config() -> FlowConfig {
        FlowConfig::new().attribute_store_name("examples-postgres")
    }

    fn read(&self, context: &mut Context) -> HandlerResult<RpcResult<UserProfile>> {
        Ok(RpcResult::new(profile().get_required(context)?))
    }

    fn update(
        &self,
        context: &mut Context,
        replacement: UserProfile,
    ) -> HandlerResult<RpcResult<UserProfile>> {
        profile().set(context, replacement.clone())?;
        Ok(RpcResult::new(replacement))
    }
}

impl Flow for UserProfileFlow {
    type StartInput = UserProfile;

    fn flow_type(&self) -> &'static str {
        "UserProfileFlow"
    }

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.create)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&profile())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(USER_PROFILE_READ, Self::read)
            .function(USER_PROFILE_UPDATE, Self::update)
    }
}

#[derive(Default)]
struct Create;

impl Step for Create {
    type Input = UserProfile;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        profile().set(context, input)?;
        Ok(StepDecision::dead_end())
    }
}

fn profile() -> Attribute<UserProfile> {
    Attribute::new("user-profile").sync_to_attribute_store()
}
