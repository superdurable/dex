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

use std::sync::LazyLock;
use std::thread;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use dex_sdk::{
    Channel, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, RetryPolicy, Step,
    StepDecision, StepList, StepMovement, StepOptions, Wait,
};
use serde::{Deserialize, Serialize};

const CONTRACT_EDIT_DURATION: Duration = Duration::from_millis(100);
const CONTRACT_TOKEN: &str = "contract";

/// One reviewer's turn to edit the shared contract.
#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ContractEdit {
    pub reviewer: String,
    pub should_fail_edit: bool,
    pub started_at_millis: i64,
}

/// Reports when one edit held the contract and how it ended.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ContractEditRecord {
    pub reviewer: String,
    pub outcome: String,
    pub started_at_millis: i64,
    pub finished_at_millis: i64,
}

pub static CONTRACT_EDIT_TOKEN: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("contractEditToken"));
pub static CONTRACT_EDIT_FINISHED: LazyLock<Channel<ContractEditRecord>> =
    LazyLock::new(|| Channel::new("contractEditFinished"));

/// Lets several reviewers edit one contract, one at a time.
/// A one-message Channel is the token that guards the two-Step edit section.
#[derive(Default)]
pub struct ContractEditFlow {
    start_contract_edits: StartContractEdits,
    enter_contract_edit: EnterContractEdit,
    save_contract_edit: SaveContractEdit,
    release_contract_edit_token: ReleaseContractEditToken,
    finish_contract_edits: FinishContractEdits,
}

impl Flow for ContractEditFlow {
    type StartInput = Vec<ContractEdit>;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_contract_edits)
            .and(&self.enter_contract_edit)
            .and(&self.save_contract_edit)
            .and(&self.release_contract_edit_token)
            .and(&self.finish_contract_edits)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&CONTRACT_EDIT_TOKEN)
            .channel(&CONTRACT_EDIT_FINISHED)
    }
}

/// Publishes the only token and starts one branch per reviewer.
#[derive(Default)]
struct StartContractEdits;

impl Step for StartContractEdits {
    type Input = Vec<ContractEdit>;

    fn execute(&self, context: &mut Context, edits: Self::Input) -> HandlerResult<StepDecision> {
        CONTRACT_EDIT_TOKEN.publish(context, CONTRACT_TOKEN.to_string())?;
        let edit_count = edits.len();
        let edit_branches = edits
            .into_iter()
            .map(|edit| StepMovement::to(&EnterContractEdit, edit));
        Ok(StepDecision::go_to_many(edit_branches.chain([
            StepMovement::to(&FinishContractEdits, edit_count),
        ])))
    }
}

/// Enters the section by consuming the token.
#[derive(Default)]
struct EnterContractEdit;

impl Step for EnterContractEdit {
    type Input = ContractEdit;

    /// Routes an Execute failure after the token was consumed to the Step
    /// that publishes it back.
    fn options(&self) -> StepOptions<Self::Input> {
        contract_edit_options()
    }

    fn wait_for(&self, _context: &mut Context, _edit: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(CONTRACT_EDIT_TOKEN.for_one()))
    }

    fn execute(
        &self,
        _context: &mut Context,
        mut edit: Self::Input,
    ) -> HandlerResult<StepDecision> {
        edit.started_at_millis = current_unix_millis()?;
        thread::sleep(CONTRACT_EDIT_DURATION);
        Ok(StepDecision::go_to(&SaveContractEdit, edit))
    }
}

/// The last Step of the section; it publishes the token back.
#[derive(Default)]
struct SaveContractEdit;

impl Step for SaveContractEdit {
    type Input = ContractEdit;

    fn options(&self) -> StepOptions<Self::Input> {
        contract_edit_options()
    }

    fn execute(&self, context: &mut Context, edit: Self::Input) -> HandlerResult<StepDecision> {
        thread::sleep(CONTRACT_EDIT_DURATION);
        if edit.should_fail_edit {
            return Err(HandlerError::new(
                "ContractEditFailed",
                "contract edit failed",
            ));
        }
        leave_contract_edit(context, edit, "saved")?;
        Ok(StepDecision::dead_end())
    }
}

/// The failure exit of the section.
#[derive(Default)]
struct ReleaseContractEditToken;

impl Step for ReleaseContractEditToken {
    type Input = ContractEdit;

    fn execute(&self, context: &mut Context, edit: Self::Input) -> HandlerResult<StepDecision> {
        leave_contract_edit(context, edit, "failed")?;
        Ok(StepDecision::dead_end())
    }
}

/// Joins the branches after every reviewer leaves the section.
#[derive(Default)]
struct FinishContractEdits;

impl Step for FinishContractEdits {
    type Input = usize;

    fn wait_for(&self, _context: &mut Context, edit_count: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(CONTRACT_EDIT_FINISHED.for_n(edit_count)))
    }

    fn execute(
        &self,
        context: &mut Context,
        _edit_count: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let records = CONTRACT_EDIT_FINISHED.condition_results(context)?;
        Ok(StepDecision::graceful_complete(records))
    }
}

fn contract_edit_options() -> StepOptions<ContractEdit> {
    StepOptions::new()
        .execute_retry(
            RetryPolicy::new()
                .initial_interval(Duration::from_secs(1))
                .maximum_attempts(2),
        )
        .on_execute_failure_proceed_to(&ReleaseContractEditToken)
}

/// Records the edit and publishes the token for the next reviewer.
fn leave_contract_edit(
    context: &mut Context,
    edit: ContractEdit,
    outcome: &str,
) -> HandlerResult<()> {
    CONTRACT_EDIT_FINISHED.publish(
        context,
        ContractEditRecord {
            reviewer: edit.reviewer,
            outcome: outcome.to_string(),
            started_at_millis: edit.started_at_millis,
            finished_at_millis: current_unix_millis()?,
        },
    )?;
    CONTRACT_EDIT_TOKEN.publish(context, CONTRACT_TOKEN.to_string())
}

fn current_unix_millis() -> HandlerResult<i64> {
    let elapsed = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(HandlerError::from_error)?;
    i64::try_from(elapsed.as_millis()).map_err(HandlerError::from_error)
}
