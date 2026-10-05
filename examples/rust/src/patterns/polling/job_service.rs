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

use std::collections::{HashMap, HashSet};
use std::error::Error;
use std::fmt::{Display, Formatter};
use std::sync::{Mutex, MutexGuard};
use std::time::{Duration, Instant};

use serde::{Deserialize, Serialize};

const FAKE_JOB_DURATION: Duration = Duration::from_secs(6);

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum JobState {
    Queued,
    Running,
    Succeeded,
    Failed,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct JobStatus {
    #[serde(rename = "jobId")]
    pub job_id: String,
    pub state: JobState,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub result: String,
}

#[derive(Debug)]
pub struct JobServiceError {
    message: String,
}

impl Display for JobServiceError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(&self.message)
    }
}

impl Error for JobServiceError {}

/// FakeJobService stands in for an external job API, such as a cloud build service.
#[derive(Default)]
pub struct FakeJobService {
    records: Mutex<JobRecords>,
}

#[derive(Default)]
struct JobRecords {
    started_at: HashMap<String, Instant>,
    completed_job_ids: HashSet<String>,
}

impl FakeJobService {
    /// Starts a job idempotently per job ID, so a retried Step does not start a second job.
    pub fn start_job(&self, job_id: &str, _timeout: Duration) -> Result<(), JobServiceError> {
        self.lock_records()
            .started_at
            .entry(job_id.to_string())
            .or_insert_with(Instant::now);
        Ok(())
    }

    pub fn get_job_status(
        &self,
        job_id: &str,
        _timeout: Duration,
    ) -> Result<JobStatus, JobServiceError> {
        let records = self.lock_records();
        let started_at = records
            .started_at
            .get(job_id)
            .ok_or_else(|| job_not_found(job_id))?;
        let elapsed = started_at.elapsed();
        let (state, result) =
            if records.completed_job_ids.contains(job_id) || elapsed >= FAKE_JOB_DURATION {
                (JobState::Succeeded, format!("artifact for {job_id}"))
            } else if elapsed >= FAKE_JOB_DURATION / 3 {
                (JobState::Running, String::new())
            } else {
                (JobState::Queued, String::new())
            };
        Ok(JobStatus {
            job_id: job_id.to_string(),
            state,
            result,
        })
    }

    /// Finishes a job early so callers do not wait for its full duration.
    pub fn complete_job(&self, job_id: &str) -> Result<(), JobServiceError> {
        let mut records = self.lock_records();
        if !records.started_at.contains_key(job_id) {
            return Err(job_not_found(job_id));
        }
        records.completed_job_ids.insert(job_id.to_string());
        Ok(())
    }

    fn lock_records(&self) -> MutexGuard<'_, JobRecords> {
        self.records
            .lock()
            .expect("fake job records lock is poisoned")
    }
}

fn job_not_found(job_id: &str) -> JobServiceError {
    JobServiceError {
        message: format!("job {job_id} not found"),
    }
}
