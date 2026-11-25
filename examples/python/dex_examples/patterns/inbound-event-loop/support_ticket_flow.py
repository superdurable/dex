# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Take ticket replies until the ticket is resolved or goes quiet."""

from __future__ import annotations

from collections.abc import Sequence
from dataclasses import dataclass, field
from datetime import timedelta

from dex import (
    AttributeMap,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    StepOptions,
    Timer,
    Wait,
    force_complete_if_channels_empty,
    go_to,
    rpc,
)

INACTIVITY_TIMEOUT = timedelta(hours=24)
MAX_REPLY_LENGTH = 4000
CURRENT_TRANSCRIPT_CHUNK = "current"
TRANSCRIPT_CHUNK_SIZE = 50


@dataclass
class TicketReply:
    sequence: int
    text: str


@dataclass
class TranscriptChunk:
    replies: list[TicketReply] = field(default_factory=list)


TICKET_REPLIES = Channel("ticketReplies", str)
TICKET_RESOLUTIONS = Channel("ticketResolutions", str)
TRANSCRIPT_CHUNKS = AttributeMap("transcriptChunks", TranscriptChunk)


# dex:group group-id:inbound group-label:"Inbound messages"
# dex:explanation text:"Record each batch of replies, or route on the resolution or the inactivity Timer."
class AwaitTicketMessage(Step[str]):
    """Waits for replies, a resolution, or inactivity; the input is a pending resolution."""

    def wait_for(self, context: Context, pending_resolution: str) -> Wait:
        if pending_resolution:
            return Wait.until(TICKET_REPLIES.at_least(1))
        # Replies come before the resolution, so earlier replies are recorded first.
        return Wait.any_of(
            TICKET_REPLIES.at_least(1),
            TICKET_RESOLUTIONS.for_one(),
            Timer.by_duration(INACTIVITY_TIMEOUT),
        )

    def execute(self, context: Context, pending_resolution: str) -> StepDecision:
        replies = TICKET_REPLIES.results(context)
        if replies:
            append_to_transcript(context, replies)
            if pending_resolution:
                return go_to(CloseResolvedTicket, pending_resolution)
            # A new execution registers a new wait, so the Timer restarts.
            return go_to(AwaitTicketMessage, "")
        resolutions = TICKET_RESOLUTIONS.results(context)
        if resolutions:
            return go_to(CloseResolvedTicket, resolutions[0])
        if context.has_timer_fired():
            return go_to(CloseInactiveTicket, None)
        raise RuntimeError("no Condition of the ticket wait completed")

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_load_attribute_map_instances=(
                TRANSCRIPT_CHUNKS.load(CURRENT_TRANSCRIPT_CHUNK),
            )
        )


# dex:group group-id:resolution group-label:"Resolution"
# dex:explanation text:"Close the resolved ticket once no reply is left to record."
class CloseResolvedTicket(Step[str]):
    def execute(self, context: Context, reason: str) -> StepDecision:
        return force_complete_if_channels_empty(
            f"resolved: {reason}",
            StepMovement.of(AwaitTicketMessage, reason),
            TICKET_REPLIES,
        )


# dex:group group-id:inactivity group-label:"Inactivity"
# dex:explanation text:"Close the quiet ticket once no reply or resolution is waiting."
class CloseInactiveTicket(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return force_complete_if_channels_empty(
            "closed after inactivity",
            StepMovement.of(AwaitTicketMessage, ""),
            TICKET_REPLIES,
            TICKET_RESOLUTIONS,
        )


class SupportTicketFlow(Flow[str]):
    """Records ticket replies until a resolution arrives or the ticket goes quiet."""

    def __init__(self) -> None:
        self.await_ticket_message = AwaitTicketMessage()
        self.close_resolved_ticket = CloseResolvedTicket()
        self.close_inactive_ticket = CloseInactiveTicket()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.await_ticket_message).other_steps(
            self.close_resolved_ticket, self.close_inactive_ticket
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(TICKET_REPLIES, TICKET_RESOLUTIONS, TRANSCRIPT_CHUNKS)

    @rpc
    def send_ticket_reply(self, context: Context, text: str) -> None:
        """Validates one reply and queues it for the waiting Step."""
        if not text.strip():
            raise ValueError("reply text is required")
        if len(text) > MAX_REPLY_LENGTH:
            raise ValueError(f"reply text exceeds {MAX_REPLY_LENGTH} characters")
        TICKET_REPLIES.publish(context, text)

    @rpc
    def resolve_ticket(self, context: Context, reason: str) -> None:
        """Queues the resolution; an empty reason would mean no resolution."""
        if not reason.strip():
            raise ValueError("resolution reason is required")
        TICKET_RESOLUTIONS.publish(context, reason)


def append_to_transcript(context: Context, texts: Sequence[str]) -> None:
    """Appends replies to the current chunk and archives each full chunk."""
    chunk = TRANSCRIPT_CHUNKS.get(context, CURRENT_TRANSCRIPT_CHUNK) or TranscriptChunk()
    next_sequence = chunk.replies[-1].sequence + 1 if chunk.replies else 1
    for text in texts:
        if len(chunk.replies) == TRANSCRIPT_CHUNK_SIZE:
            archive_token = archive_chunk_token(chunk.replies[0].sequence)
            TRANSCRIPT_CHUNKS.set(context, archive_token, chunk)
            chunk = TranscriptChunk()
        chunk.replies.append(TicketReply(next_sequence, text))
        next_sequence += 1
    TRANSCRIPT_CHUNKS.set(context, CURRENT_TRANSCRIPT_CHUNK, chunk)


def archive_chunk_token(first_sequence: int) -> str:
    return f"{first_sequence:020d}"
