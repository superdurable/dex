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

//! Iteration: Arbitrary Cycles (WCP-10), Structured Loop (WCP-21), and
//! Recursion (WCP-22).

use dex_sdk::{
    Context, Flow, HandlerError, HandlerResult, Step, StepDecision, StepList, SubFlow, Wait,
};
use serde::{Deserialize, Serialize};

use super::extend_trail;

/// How the author submits an article.
#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ArticleSubmission {
    pub has_draft: bool,
    pub should_abandon: bool,
}

/// The current revision and the Step types run so far.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ArticleRevision {
    pub revision: u32,
    pub should_abandon: bool,
    pub trail: Vec<String>,
}

const FINAL_ARTICLE_REVISION: u32 = 1;

/// WCP-10 Arbitrary Cycles: the draft-edit cycle has two entry points and
/// two exit points, and GoTo may target a Step that already ran.
#[derive(Default)]
pub struct ArbitraryCyclesFlow {
    route_submission: RouteSubmission,
    draft_article: DraftArticle,
    edit_article: EditArticle,
    publish_article: PublishArticle,
    abandon_article: AbandonArticle,
}

impl Flow for ArbitraryCyclesFlow {
    type StartInput = ArticleSubmission;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.route_submission)
            .and(&self.draft_article)
            .and(&self.edit_article)
            .and(&self.publish_article)
            .and(&self.abandon_article)
    }
}

#[derive(Default)]
struct RouteSubmission;

impl Step for RouteSubmission {
    type Input = ArticleSubmission;

    fn execute(
        &self,
        _context: &mut Context,
        submission: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let revision = ArticleRevision {
            revision: 0,
            should_abandon: submission.should_abandon,
            trail: vec![self.step_type().to_string()],
        };
        if submission.has_draft {
            return Ok(StepDecision::go_to(&EditArticle, revision));
        }
        Ok(StepDecision::go_to(&DraftArticle, revision))
    }
}

#[derive(Default)]
struct DraftArticle;

impl Step for DraftArticle {
    type Input = ArticleRevision;

    fn execute(
        &self,
        _context: &mut Context,
        revision: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let trail = extend_trail(revision.trail, self.step_type());
        if revision.should_abandon && revision.revision >= FINAL_ARTICLE_REVISION {
            return Ok(StepDecision::go_to(&AbandonArticle, trail));
        }
        Ok(StepDecision::go_to(
            &EditArticle,
            ArticleRevision { trail, ..revision },
        ))
    }
}

#[derive(Default)]
struct EditArticle;

impl Step for EditArticle {
    type Input = ArticleRevision;

    fn execute(
        &self,
        _context: &mut Context,
        revision: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let trail = extend_trail(revision.trail, self.step_type());
        if revision.revision >= FINAL_ARTICLE_REVISION {
            return Ok(StepDecision::go_to(&PublishArticle, trail));
        }
        Ok(StepDecision::go_to(
            &DraftArticle,
            ArticleRevision {
                revision: revision.revision + 1,
                should_abandon: revision.should_abandon,
                trail,
            },
        ))
    }
}

#[derive(Default)]
struct PublishArticle;

impl Step for PublishArticle {
    type Input = Vec<String>;

    fn execute(&self, _context: &mut Context, trail: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(extend_trail(
            trail,
            self.step_type(),
        )))
    }
}

#[derive(Default)]
struct AbandonArticle;

impl Step for AbandonArticle {
    type Input = Vec<String>;

    fn execute(&self, _context: &mut Context, trail: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(extend_trail(
            trail,
            self.step_type(),
        )))
    }
}

/// The batches still to process and those already processed.
#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct BatchProgress {
    pub remaining_batches: u32,
    pub processed_batches: Vec<String>,
}

/// WCP-21 Structured Loop: a pre-test Step moves to the loop body or the
/// exit, and the body moves back to the test.
#[derive(Default)]
pub struct StructuredLoopFlow {
    check_batches: CheckBatches,
    process_batch: ProcessBatch,
    finish_batches: FinishBatches,
}

impl Flow for StructuredLoopFlow {
    type StartInput = BatchProgress;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.check_batches)
            .and(&self.process_batch)
            .and(&self.finish_batches)
    }
}

#[derive(Default)]
struct CheckBatches;

impl Step for CheckBatches {
    type Input = BatchProgress;

    fn execute(
        &self,
        _context: &mut Context,
        progress: Self::Input,
    ) -> HandlerResult<StepDecision> {
        if progress.remaining_batches == 0 {
            return Ok(StepDecision::go_to(
                &FinishBatches,
                progress.processed_batches,
            ));
        }
        Ok(StepDecision::go_to(&ProcessBatch, progress))
    }
}

#[derive(Default)]
struct ProcessBatch;

impl Step for ProcessBatch {
    type Input = BatchProgress;

    fn execute(
        &self,
        _context: &mut Context,
        mut progress: Self::Input,
    ) -> HandlerResult<StepDecision> {
        progress
            .processed_batches
            .push(format!("batch-{}", progress.remaining_batches));
        Ok(StepDecision::go_to(
            &CheckBatches,
            BatchProgress {
                remaining_batches: progress.remaining_batches - 1,
                processed_batches: progress.processed_batches,
            },
        ))
    }
}

#[derive(Default)]
struct FinishBatches;

impl Step for FinishBatches {
    type Input = Vec<String>;

    fn execute(
        &self,
        _context: &mut Context,
        processed_batches: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(processed_batches))
    }
}

/// WCP-22 Recursion: the Step waits for a SubFlow of its own Flow type.
#[derive(Default)]
pub struct FactorialRecursionFlow {
    compute_factorial: ComputeFactorial,
}

impl Flow for FactorialRecursionFlow {
    type StartInput = i64;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.compute_factorial)
    }
}

#[derive(Default)]
struct ComputeFactorial;

impl Step for ComputeFactorial {
    type Input = i64;

    fn wait_for(&self, _context: &mut Context, n: Self::Input) -> HandlerResult<Wait> {
        if n <= 1 {
            return Ok(Wait::skip_immediately());
        }
        let smaller_factorial = SubFlow::run(&FactorialRecursionFlow::default(), n - 1)
            .map_err(HandlerError::from_error)?;
        Ok(Wait::until(smaller_factorial))
    }

    fn execute(&self, context: &mut Context, n: Self::Input) -> HandlerResult<StepDecision> {
        if n <= 1 {
            return Ok(StepDecision::graceful_complete(1_i64));
        }
        let smaller_factorial: i64 = SubFlow::condition_result(context)?
            .single_output()
            .map_err(HandlerError::from_error)?;
        Ok(StepDecision::graceful_complete(n * smaller_factorial))
    }
}
