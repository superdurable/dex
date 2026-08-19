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
    Attribute, AttributeIndex, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList,
    RpcResult, Step, StepDecision, StepList,
};
use serde::{Deserialize, Serialize};

pub const JOB_POST_READ: Rpc<(), JobPost> = Rpc::new("JobPostRead");
pub const JOB_POST_UPDATE: Rpc<JobPost, JobPost> = Rpc::new("JobPostUpdate");
pub const JOB_POST_DELETE: Rpc<String, ()> = Rpc::new("JobPostDelete");

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
pub struct JobPost {
    pub title: String,
    pub description: String,
    pub notes: String,
    pub deleted: bool,
}

#[derive(Default)]
pub struct JobPostFlow {
    create: Create,
}

impl JobPostFlow {
    fn read(&self, context: &mut Context) -> HandlerResult<RpcResult<JobPost>> {
        Ok(RpcResult::new(post().get_required(context)?))
    }

    fn update(
        &self,
        context: &mut Context,
        replacement: JobPost,
    ) -> HandlerResult<RpcResult<JobPost>> {
        post().set(context, replacement.clone())?;
        title().set(context, replacement.title.clone())?;
        description().set(context, replacement.description.clone())?;
        Ok(RpcResult::new(replacement))
    }

    fn delete(&self, context: &mut Context, notes: String) -> HandlerResult<()> {
        let mut current = post().get_required(context)?;
        current.deleted = true;
        current.notes = notes;
        post().set(context, current)
    }
}

impl Flow for JobPostFlow {
    type StartInput = JobPost;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.create)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&post())
            .attribute(&title())
            .attribute(&description())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(JOB_POST_READ, Self::read)
            .function(JOB_POST_UPDATE, Self::update)
            .procedure(JOB_POST_DELETE, Self::delete)
    }
}

#[derive(Default)]
struct Create;

impl Step for Create {
    type Input = JobPost;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        post().set(context, input.clone())?;
        title().set(context, input.title)?;
        description().set(context, input.description)?;
        Ok(StepDecision::dead_end())
    }
}

fn post() -> Attribute<JobPost> {
    Attribute::new("job-post")
}

fn title() -> Attribute<String> {
    Attribute::new("job-post-title").indexed(AttributeIndex::full_text())
}

fn description() -> Attribute<String> {
    Attribute::new("job-post-description").indexed(AttributeIndex::full_text())
}
