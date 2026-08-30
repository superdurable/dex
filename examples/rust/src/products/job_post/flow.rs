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

use std::{sync::LazyLock, time::Duration};

use dex_sdk::{
    Attribute, AttributeIndex, Context, Flow, HandlerResult, PersistenceSchema, RetryPolicy, Rpc,
    RpcList, RpcResult, Step, StepDecision, StepList, StepMovement, StepOptions,
};
use serde::{Deserialize, Serialize};

use crate::shared::MyDependencyService;

pub const JOB_POST_READ: Rpc<(), JobPost> = Rpc::new("JobPostRead");
pub const JOB_POST_UPDATE: Rpc<JobPost, JobPost> = Rpc::new("JobPostUpdate");
pub const JOB_POST_DELETE: Rpc<String, ()> = Rpc::new("JobPostDelete");

#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq, Serialize)]
pub struct JobPost {
    pub title: String,
    pub description: String,
    pub notes: String,
    pub deleted: bool,
}

#[derive(Default)]
pub struct JobPostingFlow {
    create: Create,
    update_linkedin_posting: UpdateLinkedInPosting,
    update_indeed_posting: UpdateIndeedPosting,
}

impl JobPostingFlow {
    fn read(&self, context: &mut Context) -> HandlerResult<RpcResult<JobPost>> {
        Ok(RpcResult::new(POST.get_required(context)?))
    }

    fn update(
        &self,
        context: &mut Context,
        replacement: JobPost,
    ) -> HandlerResult<RpcResult<JobPost>> {
        POST.set(context, replacement.clone())?;
        TITLE.set(context, replacement.title.clone())?;
        DESCRIPTION.set(context, replacement.description.clone())?;
        Ok(RpcResult::new(replacement)
            .then(StepMovement::to(&self.update_linkedin_posting, ()))
            .then(StepMovement::to(&self.update_indeed_posting, ())))
    }

    fn delete(&self, context: &mut Context, notes: String) -> HandlerResult<()> {
        let mut current = POST.get_required(context)?;
        current.deleted = true;
        current.notes = notes;
        POST.set(context, current)
    }
}

impl Flow for JobPostingFlow {
    type StartInput = JobPost;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.create)
            .and(&self.update_linkedin_posting)
            .and(&self.update_indeed_posting)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&POST)
            .attribute(&TITLE)
            .attribute(&DESCRIPTION)
            .attribute(&UPDATE_LINKEDIN_POSTING_LOCK)
            .attribute(&UPDATE_INDEED_POSTING_LOCK)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(JOB_POST_READ, Self::read)
            .function(JOB_POST_UPDATE.lock(TITLE.lock()), Self::update)
            .procedure(JOB_POST_DELETE, Self::delete)
    }
}

#[derive(Default)]
pub struct Create;

impl Step for Create {
    type Input = JobPost;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        POST.set(context, input.clone())?;
        TITLE.set(context, input.title)?;
        DESCRIPTION.set(context, input.description)?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
pub struct UpdateLinkedInPosting {
    service: MyDependencyService,
}

impl Step for UpdateLinkedInPosting {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let posting = POST.get_required(context)?;
        self.service
            .update_external_system(&format!("update LinkedIn job posting: {}", posting.title));
        Ok(StepDecision::dead_end())
    }

    fn options(&self) -> StepOptions<Self::Input> {
        job_board_update_options().execute_lock(UPDATE_LINKEDIN_POSTING_LOCK.lock())
    }
}

#[derive(Default)]
pub struct UpdateIndeedPosting {
    service: MyDependencyService,
}

impl Step for UpdateIndeedPosting {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let posting = POST.get_required(context)?;
        self.service
            .update_external_system(&format!("update Indeed job posting: {}", posting.title));
        Ok(StepDecision::dead_end())
    }

    fn options(&self) -> StepOptions<Self::Input> {
        job_board_update_options().execute_lock(UPDATE_INDEED_POSTING_LOCK.lock())
    }
}

fn job_board_update_options() -> StepOptions<()> {
    StepOptions::new().execute_retry(
        RetryPolicy::new()
            .initial_interval(Duration::from_secs(3))
            .backoff_coefficient(2.0)
            .maximum_interval(Duration::from_secs(60))
            .maximum_attempts(100)
            .total_duration(Duration::from_secs(60 * 60)),
    )
}

static POST: LazyLock<Attribute<JobPost>> = LazyLock::new(|| Attribute::new("job-post"));

static TITLE: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("job-post-title").indexed(AttributeIndex::full_text()));

static DESCRIPTION: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("job-post-description").indexed(AttributeIndex::full_text()));

static UPDATE_LINKEDIN_POSTING_LOCK: LazyLock<Attribute<()>> =
    LazyLock::new(|| Attribute::new("UpdateLinkedInPostingLock"));

static UPDATE_INDEED_POSTING_LOCK: LazyLock<Attribute<()>> =
    LazyLock::new(|| Attribute::new("UpdateIndeedPostingLock"));
