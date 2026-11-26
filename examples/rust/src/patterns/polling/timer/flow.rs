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

use std::sync::Arc;
use std::time::Duration;

use dex_sdk::{
    Context, Flow, HandlerError, HandlerResult, PersistenceSchema, RetryPolicy, Step, StepDecision,
    StepList, StepOptions, Timer, Wait,
};
use serde::{Deserialize, Serialize};

use crate::patterns::polling::job_service::{FakeJobService, JobState};

// POLL_INTERVAL * MAX_POLL_ROUNDS bounds the whole wait.
const POLL_INTERVAL: Duration = Duration::from_secs(10);
pub const POLL_INTERVAL_TIMER_ID: &str = "poll-interval";
const MAX_POLL_ROUNDS: u32 = 60;
const JOB_CALL_TIMEOUT: Duration = Duration::from_secs(10);

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JobPollRound {
    pub job_id: String,
    pub round_number: u32,
}

pub struct CheckJobStatus {
    jobs: Arc<FakeJobService>,
}

impl CheckJobStatus {
    pub fn new(jobs: Arc<FakeJobService>) -> Self {
        Self { jobs }
    }
}

impl Step for CheckJobStatus {
    type Input = JobPollRound;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_retry(
                RetryPolicy::new()
                    .initial_interval(Duration::from_secs(1))
                    .backoff_coefficient(2.0)
                    .maximum_attempts(3),
            )
            .on_execute_failure_proceed_to(&RecordJobWaitFailure)
    }

    fn wait_for(&self, _context: &mut Context, _poll_round: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(
            Timer::by_duration(POLL_INTERVAL).with_id(POLL_INTERVAL_TIMER_ID),
        ))
    }

    fn execute(
        &self,
        _context: &mut Context,
        poll_round: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let job_id = &poll_round.job_id;
        let status = self
            .jobs
            .get_job_status(job_id, JOB_CALL_TIMEOUT)
            .map_err(HandlerError::from_error)?;
        match status.state {
            JobState::Succeeded => return Ok(StepDecision::graceful_complete(status.result)),
            JobState::Failed => {
                return Ok(StepDecision::force_fail(format!("job {job_id} failed")));
            }
            JobState::Queued | JobState::Running => {}
        }
        if poll_round.round_number >= MAX_POLL_ROUNDS {
            return Ok(StepDecision::force_fail(format!(
                "job {job_id} did not finish in {MAX_POLL_ROUNDS} rounds"
            )));
        }
        let next_round = JobPollRound {
            job_id: poll_round.job_id.clone(),
            round_number: poll_round.round_number + 1,
        };
        Ok(StepDecision::go_to(self, next_round))
    }
}

struct RecordJobWaitFailure;

impl Step for RecordJobWaitFailure {
    type Input = JobPollRound;

    fn execute(
        &self,
        context: &mut Context,
        poll_round: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let failure = context
            .recovery_error()
            .expect("an execute failure route provides the recovery error");
        Ok(StepDecision::force_fail(format!(
            "checking job {} failed: {}: {}",
            poll_round.job_id,
            failure.error_type(),
            failure.detail()
        )))
    }
}

pub struct PollingWithTimerFlow {
    start_job: StartJob,
    check_job_status: CheckJobStatus,
    record_job_wait_failure: RecordJobWaitFailure,
}

impl PollingWithTimerFlow {
    pub fn new(jobs: Arc<FakeJobService>) -> Self {
        Self {
            start_job: StartJob {
                jobs: Arc::clone(&jobs),
            },
            check_job_status: CheckJobStatus { jobs },
            record_job_wait_failure: RecordJobWaitFailure,
        }
    }
}

impl Flow for PollingWithTimerFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_job)
            .and(&self.check_job_status)
            .and(&self.record_job_wait_failure)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
    }
}

struct StartJob {
    jobs: Arc<FakeJobService>,
}

impl Step for StartJob {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let job_id = context.flow_id().to_string();
        self.jobs
            .start_job(&job_id, JOB_CALL_TIMEOUT)
            .map_err(HandlerError::from_error)?;
        let check_job_status = CheckJobStatus {
            jobs: Arc::clone(&self.jobs),
        };
        let first_round = JobPollRound {
            job_id,
            round_number: 1,
        };
        Ok(StepDecision::go_to(&check_job_status, first_round))
    }
}
