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

"""A support ticket Flow whose Worker and Client encrypt payloads."""

from __future__ import annotations

import asyncio

from dex import (
    AesGcmPayloadCodec,
    AsyncClient,
    AsyncWorker,
    Attribute,
    BlobCache,
    ClientOptions,
    Context,
    FilePayloadEncryptionKeyProvider,
    Flow,
    PayloadEncryptionOptions,
    PersistenceSchema,
    PullWorkerOptions,
    Registry,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    WorkerOptions,
    graceful_complete,
    rpc,
)

# The customer's text, so payload encryption stores it as ciphertext.
TICKET_MESSAGE = Attribute("support-ticket-message", str)
# Stays plaintext, so wait_for_attribute_match and Action conditions can compare it.
TICKET_STATUS = Attribute("support-ticket-status", str, plaintext=True)


class ReceiveTicketStep(Step[str]):
    def execute(self, context: Context, message: str) -> StepDecision:
        TICKET_MESSAGE.set(context, message)
        TICKET_STATUS.set(context, "received")
        return graceful_complete("received")


class SupportTicketFlow(Flow[str]):
    def __init__(self) -> None:
        self.receive_ticket_step = ReceiveTicketStep()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.receive_ticket_step)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(TICKET_MESSAGE, TICKET_STATUS)

    @rpc
    def get_ticket_message(self, context: Context) -> RPCResult[str]:
        return RPCResult(TICKET_MESSAGE.get(context))


def new_file_payload_encryption(key_file_path: str) -> PayloadEncryptionOptions:
    """Reads a key file, such as one from dexcli codec-server keygen."""
    key_provider = FilePayloadEncryptionKeyProvider(key_file_path)
    return PayloadEncryptionOptions(AesGcmPayloadCodec(key_provider))


async def start_encrypted_support_ticket(
    cache: BlobCache, server_address: str, key_file_path: str
) -> tuple[SupportTicketFlow, AsyncWorker, AsyncClient, asyncio.Task[None]]:
    encryption = new_file_payload_encryption(key_file_path)
    flow = SupportTicketFlow()
    registry = Registry((flow,), allow_async_handlers=True)
    worker = AsyncWorker(
        registry,
        cache,
        WorkerOptions(
            server_address=server_address,
            pull=PullWorkerOptions(max_concurrent_tasks=8),
            payload_encryption=encryption,
        ),
    )
    worker_task = asyncio.create_task(worker.start())
    client = AsyncClient(
        registry,
        cache,
        ClientOptions(server_address=server_address, payload_encryption=encryption),
    )
    try:
        await worker.wait_pull_registered(timeout=30)
    except BaseException:
        await stop_encrypted_support_ticket(worker, client, worker_task)
        raise
    return flow, worker, client, worker_task


async def stop_encrypted_support_ticket(
    worker: AsyncWorker, client: AsyncClient, worker_task: asyncio.Task[None]
) -> None:
    await client.close()
    await worker.close()
    await worker_task
