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

use std::sync::{Arc, LazyLock};
use std::thread;
use std::time::{Duration, SystemTime};

use dex_sdk::{
    Context, Flow, HandlerError, HandlerResult, PersistenceSchema, RetryPolicy, Step, StepDecision,
    StepList, StepOptions, Stream,
};

use crate::patterns::polling::job_service::{FakeJobService, JobState, JobStatus};

// Keep POLL_INTERVAL + JOB_CALL_TIMEOUT <= heartbeat_timeout - 10s.
const POLL_INTERVAL: Duration = Duration::from_secs(2);
const JOB_CALL_TIMEOUT: Duration = Duration::from_secs(10);
const JOB_WAIT_BUDGET: Duration = Duration::from_secs(5 * 60);

pub static JOB_PROGRESS: LazyLock<Stream<JobStatus>> =
    LazyLock::new(|| Stream::new("JobProgress", 1 << 20));

struct AwaitJob {
    jobs: Arc<FakeJobService>,
}

impl Step for AwaitJob {
    type Input = String;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_method_timeout(Duration::from_secs(10 * 60))
            .heartbeat_timeout(Duration::from_secs(60))
            .execute_retry(
                RetryPolicy::new()
                    .initial_interval(Duration::from_secs(1))
                    .backoff_coefficient(2.0)
                    .maximum_attempts(3)
                    .total_duration(Duration::from_secs(10 * 60)),
            )
    }

    fn execute(&self, context: &mut Context, job_id: Self::Input) -> HandlerResult<StepDecision> {
        let deadline = context.first_attempt_at() + JOB_WAIT_BUDGET;
        let mut reported_state = context.last_heartbeat_value::<JobState>()?;
        loop {
            let status = self
                .jobs
                .get_job_status(&job_id, JOB_CALL_TIMEOUT)
                .map_err(HandlerError::from_error)?;
            match status.state {
                JobState::Succeeded => return Ok(StepDecision::graceful_complete(status.result)),
                JobState::Failed => {
                    return Ok(StepDecision::force_fail(format!("job {job_id} failed")));
                }
                JobState::Queued | JobState::Running => {}
            }
            if SystemTime::now() > deadline {
                return Ok(StepDecision::force_fail(format!(
                    "job {job_id} did not finish within {JOB_WAIT_BUDGET:?}"
                )));
            }
            if reported_state != Some(status.state) {
                reported_state = Some(status.state);
                JOB_PROGRESS.write(context, status)?;
            } else {
                context.record_heartbeat_value(status.state)?;
            }
            thread::sleep(POLL_INTERVAL);
        }
    }
}

pub struct PollingFlow {
    start_job: StartJob,
    await_job: AwaitJob,
}

impl PollingFlow {
    pub fn new(jobs: Arc<FakeJobService>) -> Self {
        Self {
            start_job: StartJob {
                jobs: Arc::clone(&jobs),
            },
            await_job: AwaitJob { jobs },
        }
    }
}

impl Flow for PollingFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_job).and(&self.await_job)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().stream(&JOB_PROGRESS)
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
        let await_job = AwaitJob {
            jobs: Arc::clone(&self.jobs),
        };
        Ok(StepDecision::go_to(&await_job, job_id))
    }
}
