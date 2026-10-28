// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeSet;

use dexzd_protocol::dex::AttributeWrite;
use dexzd_protocol::dexzd::v1::{
    FlowCommand, HardDeleteObjectReference, flow_command::Command, inbox_message_command,
    step_method_completed_command,
};

use crate::values::is_null_value;

/// An Attribute command cannot safely enter the durable log or apply.
#[derive(Debug, thiserror::Error)]
#[error("{detail}")]
pub struct HardDeleteAdmissionError {
    detail: String,
}

/// Rejects Attribute writes that lack the uploaded objects required by apply.
pub fn validate_hard_delete_command(command: &FlowCommand) -> Result<(), HardDeleteAdmissionError> {
    validate_command(command).map_err(|detail| HardDeleteAdmissionError { detail })
}

fn validate_command(command: &FlowCommand) -> Result<(), String> {
    match command.command.as_ref() {
        Some(Command::FlowStart(start)) => validate_writes(
            start
                .request
                .iter()
                .filter_map(|request| request.flow_start_options.as_ref())
                .flat_map(|options| &options.attributes),
            &start.hard_delete_objects,
        ),
        Some(Command::AttributesSet(set)) => validate_writes(
            set.request.iter().flat_map(|request| &request.attributes),
            &set.hard_delete_objects,
        ),
        Some(Command::RpcCommitted(rpc)) => validate_writes(
            rpc.response
                .iter()
                .flat_map(|response| &response.upsert_attributes),
            &rpc.hard_delete_objects,
        ),
        Some(Command::StepMethodCompleted(step)) => match step.response.as_ref() {
            Some(step_method_completed_command::Response::WaitFor(response)) => validate_writes(
                response.upsert_attributes.iter().chain(
                    response
                        .waiting_condition
                        .iter()
                        .flat_map(|condition| &condition.sub_flow_conditions)
                        .filter_map(|condition| condition.options.as_ref())
                        .flat_map(|options| &options.attributes),
                ),
                &step.hard_delete_objects,
            ),
            Some(step_method_completed_command::Response::Execute(response)) => {
                validate_writes(response.upsert_attributes.iter(), &step.hard_delete_objects)
            }
            None => Ok(()),
        },
        Some(Command::InboxMessage(inbox)) => match inbox.payload.as_ref() {
            Some(inbox_message_command::Payload::ChildStart(start)) => validate_writes(
                start
                    .condition
                    .iter()
                    .filter_map(|condition| condition.options.as_ref())
                    .flat_map(|options| &options.attributes),
                &start.hard_delete_objects,
            ),
            _ => Ok(()),
        },
        _ => Ok(()),
    }
}

fn validate_writes<'writes>(
    writes: impl Iterator<Item = &'writes AttributeWrite>,
    references: &[HardDeleteObjectReference],
) -> Result<(), String> {
    let mut referenced_indices = BTreeSet::new();
    for reference in references {
        if reference.object_id.is_empty()
            || !referenced_indices.insert(reference.write_index as usize)
        {
            return Err("hard-delete object references must have unique write indices and nonempty object IDs".to_owned());
        }
    }
    let mut write_count = 0;
    for (index, write) in writes.enumerate() {
        write_count += 1;
        let has_reference = referenced_indices.contains(&index);
        if has_reference && !write.is_hard_delete_on_null {
            return Err(format!(
                "Attribute {:?} has an unexpected hard-delete object",
                write.key
            ));
        }
        if write.is_hard_delete_on_null && !is_null_value(write.value.as_ref()) && !has_reference {
            return Err(format!(
                "hard-delete Attribute {:?} has no uploaded object",
                write.key
            ));
        }
    }
    if referenced_indices
        .last()
        .is_some_and(|index| *index >= write_count)
    {
        return Err("hard-delete object reference has an invalid write index".to_owned());
    }
    Ok(())
}
