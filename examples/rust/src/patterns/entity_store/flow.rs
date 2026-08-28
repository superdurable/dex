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

use std::sync::LazyLock;

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
            .initial_attribute(&DISPLAY_NAME, profile.display_name.clone())
            .initial_attribute(&EMAIL, profile.email.clone())
            .initial_attribute(&MARKETING_OPT_IN, profile.marketing_opt_in)
            .initial_attribute(&CREDITS, profile.credits)
            .initial_attribute(&WEIGHT, profile.weight)
            .initial_attribute(&LAST_LOGGED_IN_TIME, profile.last_logged_in_time.clone())
            .initial_attribute(&METADATA, profile.metadata.clone())
    }

    fn read(&self, context: &mut Context) -> HandlerResult<RpcResult<UserProfile>> {
        Ok(RpcResult::new(UserProfile {
            display_name: DISPLAY_NAME.get_required(context)?,
            email: EMAIL.get_required(context)?,
            marketing_opt_in: MARKETING_OPT_IN.get_required(context)?,
            credits: CREDITS.get_required(context)?,
            weight: WEIGHT.get_required(context)?,
            last_logged_in_time: LAST_LOGGED_IN_TIME.get_required(context)?,
            metadata: METADATA.get_required(context)?,
        }))
    }

    fn update(&self, context: &mut Context, replacement: UserProfile) -> HandlerResult<()> {
        validate_profile(&replacement)?;
        DISPLAY_NAME.set(context, replacement.display_name)?;
        EMAIL.set(context, replacement.email)?;
        MARKETING_OPT_IN.set(context, replacement.marketing_opt_in)?;
        CREDITS.set(context, replacement.credits)?;
        WEIGHT.set(context, replacement.weight)?;
        LAST_LOGGED_IN_TIME.set(context, replacement.last_logged_in_time)?;
        METADATA.set(context, replacement.metadata)
    }

    fn clear(&self, context: &mut Context) -> HandlerResult<()> {
        DISPLAY_NAME.delete(context)?;
        EMAIL.delete(context)?;
        MARKETING_OPT_IN.delete(context)?;
        CREDITS.delete(context)?;
        WEIGHT.delete(context)?;
        LAST_LOGGED_IN_TIME.delete(context)?;
        METADATA.delete(context)
    }
}

impl Flow for UserProfileFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::empty()
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&DISPLAY_NAME)
            .attribute(&EMAIL)
            .attribute(&MARKETING_OPT_IN)
            .attribute(&CREDITS)
            .attribute(&WEIGHT)
            .attribute(&LAST_LOGGED_IN_TIME)
            .attribute(&METADATA)
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

static DISPLAY_NAME: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("display_name").sync_to_attribute_store());

static EMAIL: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("email").sync_to_attribute_store());

static MARKETING_OPT_IN: LazyLock<Attribute<bool>> =
    LazyLock::new(|| Attribute::new("marketing_opt_in").sync_to_attribute_store());

static CREDITS: LazyLock<Attribute<i64>> =
    LazyLock::new(|| Attribute::new("credits").sync_to_attribute_store());

static WEIGHT: LazyLock<Attribute<f64>> =
    LazyLock::new(|| Attribute::new("weight").sync_to_attribute_store());

static LAST_LOGGED_IN_TIME: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("last_logged_in_time").sync_to_attribute_store());

static METADATA: LazyLock<Attribute<UserProfileMetadata>> =
    LazyLock::new(|| Attribute::new("metadata").sync_to_attribute_store());
