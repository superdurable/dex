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

//! Take ticket replies until the ticket is resolved or goes quiet.

use std::sync::LazyLock;
use std::time::Duration;

use dex_sdk::{
    AttributeMap, Channel, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, Rpc,
    RpcList, RpcResult, Step, StepDecision, StepList, StepMovement, StepOptions, Timer, Wait,
};
use serde::{Deserialize, Serialize};

pub const INACTIVITY_TIMEOUT: Duration = Duration::from_secs(24 * 60 * 60);
pub const MAX_REPLY_LENGTH: usize = 4000;
pub const CURRENT_TRANSCRIPT_CHUNK: &str = "current";
pub const TRANSCRIPT_CHUNK_SIZE: usize = 50;

pub const SEND_TICKET_REPLY: Rpc<String, ()> = Rpc::new("send_ticket_reply");
pub const RESOLVE_TICKET: Rpc<String, ()> = Rpc::new("resolve_ticket");
pub const GET_TICKET_TRANSCRIPT: Rpc<(), TranscriptChunk> = Rpc::new("get_ticket_transcript");

pub static TICKET_REPLIES: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("ticketReplies"));
pub static TICKET_RESOLUTIONS: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("ticketResolutions"));
pub static TRANSCRIPT_CHUNKS: LazyLock<AttributeMap<TranscriptChunk>> =
    LazyLock::new(|| AttributeMap::new("transcriptChunks"));

#[derive(Clone, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
pub struct TicketReply {
    pub sequence: u64,
    pub text: String,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
pub struct TranscriptChunk {
    pub replies: Vec<TicketReply>,
}

/// Records ticket replies until a resolution arrives or the ticket goes quiet.
#[derive(Default)]
pub struct SupportTicketFlow {
    await_ticket_message: AwaitTicketMessage,
    close_resolved_ticket: CloseResolvedTicket,
    close_inactive_ticket: CloseInactiveTicket,
}

impl SupportTicketFlow {
    /// Validates one reply and queues it for the waiting Step.
    fn send_ticket_reply(&self, context: &mut Context, text: String) -> HandlerResult<()> {
        if text.trim().is_empty() {
            return Err(HandlerError::new(
                "SupportTicketFlow",
                "reply text is required",
            ));
        }
        if text.chars().count() > MAX_REPLY_LENGTH {
            return Err(HandlerError::new(
                "SupportTicketFlow",
                format!("reply text exceeds {MAX_REPLY_LENGTH} characters"),
            ));
        }
        TICKET_REPLIES.publish(context, text)
    }

    /// Queues the resolution; an empty reason would mean no resolution.
    fn resolve_ticket(&self, context: &mut Context, reason: String) -> HandlerResult<()> {
        if reason.trim().is_empty() {
            return Err(HandlerError::new(
                "SupportTicketFlow",
                "resolution reason is required",
            ));
        }
        TICKET_RESOLUTIONS.publish(context, reason)
    }

    /// Returns the current transcript chunk while the ticket is open.
    fn get_ticket_transcript(
        &self,
        context: &mut Context,
    ) -> HandlerResult<RpcResult<TranscriptChunk>> {
        let chunk = TRANSCRIPT_CHUNKS
            .get(context, CURRENT_TRANSCRIPT_CHUNK)?
            .unwrap_or_default();
        Ok(RpcResult::new(chunk))
    }
}

impl Flow for SupportTicketFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.await_ticket_message)
            .and(&self.close_resolved_ticket)
            .and(&self.close_inactive_ticket)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&TICKET_REPLIES)
            .channel(&TICKET_RESOLUTIONS)
            .attribute_map(&TRANSCRIPT_CHUNKS)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure(SEND_TICKET_REPLY, Self::send_ticket_reply)
            .procedure(RESOLVE_TICKET, Self::resolve_ticket)
            .function_without_input(
                GET_TICKET_TRANSCRIPT
                    .load_attribute_map_instance(TRANSCRIPT_CHUNKS.load(CURRENT_TRANSCRIPT_CHUNK)),
                Self::get_ticket_transcript,
            )
    }
}

/// Waits for replies, a resolution, or inactivity; the input is a pending resolution.
// dex:group group-id:inbound group-label:"Inbound messages"
// dex:explanation text:"Record each batch of replies, or route on the resolution or the inactivity Timer."
#[derive(Default)]
pub struct AwaitTicketMessage;

impl Step for AwaitTicketMessage {
    type Input = String;

    fn wait_for(
        &self,
        _context: &mut Context,
        pending_resolution: Self::Input,
    ) -> HandlerResult<Wait> {
        if !pending_resolution.is_empty() {
            return Ok(Wait::until(TICKET_REPLIES.at_least(1)));
        }
        // Replies come before the resolution, so earlier replies are recorded first.
        Ok(Wait::any_of([
            TICKET_REPLIES.at_least(1),
            TICKET_RESOLUTIONS.for_one(),
            Timer::by_duration(INACTIVITY_TIMEOUT),
        ]))
    }

    fn execute(
        &self,
        context: &mut Context,
        pending_resolution: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let replies = TICKET_REPLIES.condition_results(context)?;
        if !replies.is_empty() {
            append_to_transcript(context, replies)?;
            if !pending_resolution.is_empty() {
                return Ok(StepDecision::go_to(
                    &CloseResolvedTicket,
                    pending_resolution,
                ));
            }
            // A new execution registers a new wait, so the Timer restarts.
            return Ok(StepDecision::go_to(&AwaitTicketMessage, String::new()));
        }
        let mut resolutions = TICKET_RESOLUTIONS.condition_results(context)?;
        if !resolutions.is_empty() {
            return Ok(StepDecision::go_to(
                &CloseResolvedTicket,
                resolutions.remove(0),
            ));
        }
        if context.has_any_timer_fired() {
            return Ok(StepDecision::go_to(&CloseInactiveTicket, ()));
        }
        Err(HandlerError::new(
            "AwaitTicketMessage",
            "no Condition of the ticket wait completed",
        ))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_load_attribute_map_instance(TRANSCRIPT_CHUNKS.load(CURRENT_TRANSCRIPT_CHUNK))
    }
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Close the resolved ticket once no reply is left to record."
#[derive(Default)]
pub struct CloseResolvedTicket;

impl Step for CloseResolvedTicket {
    type Input = String;

    fn execute(&self, _context: &mut Context, reason: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::force_complete_if_channels_empty(
            format!("resolved: {reason}"),
            StepMovement::to(&AwaitTicketMessage, reason),
            [TICKET_REPLIES.when_empty()],
        ))
    }
}

// dex:group group-id:inactivity group-label:"Inactivity"
// dex:explanation text:"Close the quiet ticket once no reply or resolution is waiting."
#[derive(Default)]
pub struct CloseInactiveTicket;

impl Step for CloseInactiveTicket {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::force_complete_if_channels_empty(
            "closed after inactivity".to_string(),
            StepMovement::to(&AwaitTicketMessage, String::new()),
            [TICKET_REPLIES.when_empty(), TICKET_RESOLUTIONS.when_empty()],
        ))
    }
}

/// Appends replies to the current chunk and archives each full chunk.
fn append_to_transcript(context: &mut Context, texts: Vec<String>) -> HandlerResult<()> {
    let mut chunk = TRANSCRIPT_CHUNKS
        .get(context, CURRENT_TRANSCRIPT_CHUNK)?
        .unwrap_or_default();
    let next_sequence = chunk.replies.last().map_or(1, |reply| reply.sequence + 1);
    for (sequence, text) in (next_sequence..).zip(texts) {
        if chunk.replies.len() == TRANSCRIPT_CHUNK_SIZE {
            let archive_token = archive_chunk_token(chunk.replies[0].sequence);
            TRANSCRIPT_CHUNKS.set(context, &archive_token, chunk)?;
            chunk = TranscriptChunk::default();
        }
        chunk.replies.push(TicketReply { sequence, text });
    }
    TRANSCRIPT_CHUNKS.set(context, CURRENT_TRANSCRIPT_CHUNK, chunk)
}

#[must_use]
pub fn archive_chunk_token(first_sequence: u64) -> String {
    format!("{first_sequence:020}")
}
