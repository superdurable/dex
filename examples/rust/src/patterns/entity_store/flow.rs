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

use std::time::Duration;

use dex_sdk::{
    Attribute, Context, Flow, FlowConfig, HandlerError, HandlerResult, PersistenceSchema, Rpc,
    RpcList, RpcResult, StartFlowOptions, StepList,
};
use serde::{Deserialize, Serialize};

pub const USER_PROFILE_READ: Rpc<(), UserProfile> = Rpc::new("UserProfileRead");
pub const USER_PROFILE_UPDATE: Rpc<UserProfile, ()> = Rpc::new("UserProfileUpdate");
pub const USER_PROFILE_CLEAR: Rpc<(), ()> = Rpc::new("UserProfileClear");

pub const STORE_NAME: &str = "entityStore";

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UserProfileMetadata {
    pub source: String,
    pub tags: Vec<String>,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UserProfile {
    pub display_name: String,
    pub email: String,
    pub marketing_opt_in: bool,
    pub credits: i64,
    pub weight: f64,
    pub last_logged_in_time: String,
    pub metadata: UserProfileMetadata,
}

pub struct UserProfileFlow;

impl UserProfileFlow {
    pub fn flow_config() -> FlowConfig {
        FlowConfig::new().attribute_store_names(vec![STORE_NAME.to_owned()])
    }

    pub fn start_options(profile: &UserProfile) -> StartFlowOptions {
        StartFlowOptions::new()
            .timeout(Duration::from_secs(3_600))
            .config_override(Self::flow_config())
            .initial_attribute(&display_name(), profile.display_name.clone())
            .initial_attribute(&email(), profile.email.clone())
            .initial_attribute(&marketing_opt_in(), profile.marketing_opt_in)
            .initial_attribute(&credits(), profile.credits)
            .initial_attribute(&weight(), profile.weight)
            .initial_attribute(&last_logged_in_time(), profile.last_logged_in_time.clone())
            .initial_attribute(&metadata(), profile.metadata.clone())
    }

    fn read(&self, context: &mut Context) -> HandlerResult<RpcResult<UserProfile>> {
        Ok(RpcResult::new(UserProfile {
            display_name: display_name().get_required(context)?,
            email: email().get_required(context)?,
            marketing_opt_in: marketing_opt_in().get_required(context)?,
            credits: credits().get_required(context)?,
            weight: weight().get_required(context)?,
            last_logged_in_time: last_logged_in_time().get_required(context)?,
            metadata: metadata().get_required(context)?,
        }))
    }

    fn update(&self, context: &mut Context, replacement: UserProfile) -> HandlerResult<()> {
        validate_profile(&replacement)?;
        display_name().set(context, replacement.display_name)?;
        email().set(context, replacement.email)?;
        marketing_opt_in().set(context, replacement.marketing_opt_in)?;
        credits().set(context, replacement.credits)?;
        weight().set(context, replacement.weight)?;
        last_logged_in_time().set(context, replacement.last_logged_in_time)?;
        metadata().set(context, replacement.metadata)
    }

    fn clear(&self, context: &mut Context) -> HandlerResult<()> {
        display_name().delete(context)?;
        email().delete(context)?;
        marketing_opt_in().delete(context)?;
        credits().delete(context)?;
        weight().delete(context)?;
        last_logged_in_time().delete(context)?;
        metadata().delete(context)
    }
}

impl Flow for UserProfileFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::empty()
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&display_name())
            .attribute(&email())
            .attribute(&marketing_opt_in())
            .attribute(&credits())
            .attribute(&weight())
            .attribute(&last_logged_in_time())
            .attribute(&metadata())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(USER_PROFILE_READ, Self::read)
            .procedure(USER_PROFILE_UPDATE, Self::update)
            .procedure_without_input(USER_PROFILE_CLEAR, Self::clear)
    }
}

fn validate_profile(profile: &UserProfile) -> HandlerResult<()> {
    if profile.display_name.trim().is_empty() {
        return Err(HandlerError::new("EntityStore", "displayName is required"));
    }
    if profile.email.trim().is_empty() {
        return Err(HandlerError::new("EntityStore", "email is required"));
    }
    if profile.last_logged_in_time.trim().is_empty() {
        return Err(HandlerError::new(
            "EntityStore",
            "lastLoggedInTime is required",
        ));
    }
    if profile.metadata.source.trim().is_empty() {
        return Err(HandlerError::new(
            "EntityStore",
            "metadata.source is required",
        ));
    }
    Ok(())
}

fn display_name() -> Attribute<String> {
    Attribute::new("display_name").sync_to_attribute_store()
}

fn email() -> Attribute<String> {
    Attribute::new("email").sync_to_attribute_store()
}

fn marketing_opt_in() -> Attribute<bool> {
    Attribute::new("marketing_opt_in").sync_to_attribute_store()
}

fn credits() -> Attribute<i64> {
    Attribute::new("credits").sync_to_attribute_store()
}

fn weight() -> Attribute<f64> {
    Attribute::new("weight").sync_to_attribute_store()
}

fn last_logged_in_time() -> Attribute<String> {
    Attribute::new("last_logged_in_time").sync_to_attribute_store()
}

fn metadata() -> Attribute<UserProfileMetadata> {
    Attribute::new("metadata").sync_to_attribute_store()
}
