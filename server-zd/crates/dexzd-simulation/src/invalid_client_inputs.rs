// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Client commands carrying values apply must reject, and the outcome each must get.

use dexzd_apply::ApplyOutcome;
use dexzd_protocol::dex::Value;
use dexzd_protocol::dexzd::v1::{
    FlowCommand, RequestDedupRecord, command_reply, flow_command::Command,
};
use prost::Message;

use crate::invariants::InvariantViolation;
use crate::model::MAX_VALUE_BYTES;

/// An AttributesSet that writes a value above the run's maximum value size.
pub const OVERSIZED_ATTRIBUTE_VALUE: &str = "oversized-attribute-value";
/// A ChannelPublish of a message value above the run's maximum value size.
pub const OVERSIZED_CHANNEL_MESSAGE: &str = "oversized-channel-message";
/// Every invalid client input class the generator proposes.
pub const INVALID_CLIENT_INPUT_CLASSES: &[&str] =
    &[OVERSIZED_ATTRIBUTE_VALUE, OVERSIZED_CHANNEL_MESSAGE];

const INVALID_ARGUMENT_GRPC_CODE: i32 = 3;
/// Apply's size rejection message; other INVALID_ARGUMENT replies, such as a replayed recorded reply, lack it.
const TOO_LARGE_MESSAGE: &str = "exceeds the maximum";

const INVALID_INPUT: &str = "invalid-client-input-rejected";

/// Returns the invalid input class of `command`, or `None` when its values are valid.
pub fn invalid_client_input(command: &FlowCommand) -> Option<&'static str> {
    match &command.command {
        Some(Command::AttributesSet(set)) => set
            .request
            .iter()
            .flat_map(|request| &request.attributes)
            .filter(|write| !write.is_hard_delete_on_null)
            .any(|write| is_oversized(write.value.as_ref()))
            .then_some(OVERSIZED_ATTRIBUTE_VALUE),
        Some(Command::ChannelPublish(publish)) => publish
            .request
            .iter()
            .flat_map(|request| &request.messages)
            .any(|message| is_oversized(message.value.as_ref()))
            .then_some(OVERSIZED_CHANNEL_MESSAGE),
        _ => None,
    }
}

/// Checks that a command of invalid input class `class` wrote nothing and replied with an error,
/// or wrote nothing and replayed exactly the reply of `recorded_request`.
///
/// `recorded_request` is the deduplication row for the command's request ID as it stood before
/// apply, or `None` when there was none.
///
/// Returns whether apply rejected it for its value size, or the violation.
pub fn check_invalid_client_input_rejected(
    class: &str,
    recorded_request: Option<&RequestDedupRecord>,
    outcome: &ApplyOutcome,
) -> Result<bool, InvariantViolation> {
    let error = match &outcome.reply.reply {
        Some(command_reply::Reply::Error(error)) if outcome.writes.is_empty() => error,
        // A repeated request ID replays its recorded reply whatever the repeat contains.
        _ if outcome.writes.is_empty() && is_recorded_reply(recorded_request, outcome) => {
            return Ok(false);
        }
        reply => {
            return Err(InvariantViolation::new(
                INVALID_INPUT,
                format!(
                    "a command with an {class} was accepted: {} writes, reply {reply:?}",
                    outcome.writes.len()
                ),
            ));
        }
    };
    Ok(error.grpc_code == INVALID_ARGUMENT_GRPC_CODE && error.message.contains(TOO_LARGE_MESSAGE))
}

fn is_recorded_reply(
    recorded_request: Option<&RequestDedupRecord>,
    outcome: &ApplyOutcome,
) -> bool {
    recorded_request.is_some_and(|record| record.reply.as_ref() == Some(&outcome.reply))
}

/// Uses apply's size measure: the encoded length of the wire value.
fn is_oversized(value: Option<&Value>) -> bool {
    value.is_some_and(|value| {
        u64::try_from(value.encoded_len()).unwrap_or(u64::MAX) > MAX_VALUE_BYTES
    })
}

#[cfg(test)]
mod tests {
    use dexzd_apply::StateWrite;
    use dexzd_keyspace::ColumnFamily;
    use dexzd_protocol::dexzd::v1::{CommandErrorReply, CommandReply, EmptyReply, RpcReply};

    use super::*;

    fn outcome(writes: Vec<StateWrite>, reply: command_reply::Reply) -> ApplyOutcome {
        ApplyOutcome {
            writes,
            effects: Vec::new(),
            reply: CommandReply { reply: Some(reply) },
            run_id: None,
        }
    }

    fn recorded(reply: command_reply::Reply) -> RequestDedupRecord {
        RequestDedupRecord {
            reply: Some(CommandReply { reply: Some(reply) }),
            ..RequestDedupRecord::default()
        }
    }

    #[test]
    fn an_accepted_oversized_value_is_a_violation() {
        let write = StateWrite::Put {
            column_family: ColumnFamily::Flow,
            key: vec![1],
            value: vec![2],
        };
        let accepted = outcome(vec![write], command_reply::Reply::Ok(EmptyReply {}));
        let violation = check_invalid_client_input_rejected(
            OVERSIZED_ATTRIBUTE_VALUE,
            Some(&recorded(command_reply::Reply::Ok(EmptyReply {}))),
            &accepted,
        )
        .expect_err("apply accepted an oversized value");
        assert_eq!(violation.invariant, INVALID_INPUT, "{violation}");
    }

    #[test]
    fn a_first_request_id_with_a_silent_ok_is_a_violation() {
        let silent = outcome(Vec::new(), command_reply::Reply::Ok(EmptyReply {}));
        let violation =
            check_invalid_client_input_rejected(OVERSIZED_ATTRIBUTE_VALUE, None, &silent)
                .expect_err("an oversized value with no recorded request must get an error reply");
        assert_eq!(violation.invariant, INVALID_INPUT, "{violation}");
    }

    #[test]
    fn a_reply_that_differs_from_the_recorded_one_is_a_violation() {
        let silent = outcome(Vec::new(), command_reply::Reply::Ok(EmptyReply {}));
        let different = recorded(command_reply::Reply::Rpc(RpcReply::default()));
        let violation = check_invalid_client_input_rejected(
            OVERSIZED_ATTRIBUTE_VALUE,
            Some(&different),
            &silent,
        )
        .expect_err("only the recorded reply may answer a repeated request ID");
        assert_eq!(violation.invariant, INVALID_INPUT, "{violation}");
    }

    #[test]
    fn a_repeated_request_id_may_replay_its_recorded_reply() {
        let replayed = outcome(Vec::new(), command_reply::Reply::Ok(EmptyReply {}));
        let record = recorded(command_reply::Reply::Ok(EmptyReply {}));
        assert_eq!(
            check_invalid_client_input_rejected(
                OVERSIZED_ATTRIBUTE_VALUE,
                Some(&record),
                &replayed
            ),
            Ok(false)
        );
    }

    #[test]
    fn a_size_rejection_counts_as_coverage() {
        let reply = |message: &str| {
            command_reply::Reply::Error(CommandErrorReply {
                grpc_code: INVALID_ARGUMENT_GRPC_CODE,
                sub_status: 0,
                message: message.to_owned(),
            })
        };
        let too_large = outcome(
            Vec::new(),
            reply("value of 5000 bytes exceeds the maximum of 4096 bytes"),
        );
        assert_eq!(
            check_invalid_client_input_rejected(OVERSIZED_ATTRIBUTE_VALUE, None, &too_large),
            Ok(true)
        );
        let other_error = outcome(Vec::new(), reply("Flow order-1 was not found"));
        assert_eq!(
            check_invalid_client_input_rejected(OVERSIZED_ATTRIBUTE_VALUE, None, &other_error),
            Ok(false)
        );
    }
}
