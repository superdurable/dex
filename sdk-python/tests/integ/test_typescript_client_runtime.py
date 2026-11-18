# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

"""A TypeScript Client calls RPCs with and without input on a Python Worker."""

from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path
from uuid import uuid4

from dex.dexpb import dex_pb2 as pb

from .environment import DexDevTestEnvironment
from .flow_service_client import flow_service_client
from .shared import unique_id
from .typescript_client_flow import TypeScriptClientNoteFlow

TYPESCRIPT_SDK_DIRECTORY = Path(__file__).resolve().parents[3] / "sdk-typescript"
TYPESCRIPT_CLIENT_SCRIPT = (
    Path(__file__).resolve().parent / "typescript" / "invoke_python_worker_rpcs.mjs"
)


def test_typescript_client_invokes_rpcs_on_a_python_worker() -> None:
    assert (
        TYPESCRIPT_SDK_DIRECTORY / "dist" / "src" / "index.js"
    ).is_file(), "build the TypeScript SDK first: npm ci && npm run build:native && npm run build"
    flow = TypeScriptClientNoteFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("typescript-client")
        environment.client.start_flow(flow, flow_id, None)
        completed = subprocess.run(
            [
                "node",
                str(TYPESCRIPT_CLIENT_SCRIPT),
                str(TYPESCRIPT_SDK_DIRECTORY),
                os.environ.get("DEX_SERVER_ADDRESS", "127.0.0.1:8801"),
                flow_id,
            ],
            capture_output=True,
            text=True,
            timeout=120,
        )
        assert completed.returncode == 0, completed.stderr
        assert json.loads(completed.stdout) == {
            "setNote": "from TypeScript",
            "noteAfterSet": "from TypeScript",
            "clearNote": None,
            "noteAfterClear": "",
        }
        environment.client.stop_flow(flow_id)


def test_python_worker_serves_an_input_free_rpc_whose_input_is_unset() -> None:
    flow = TypeScriptClientNoteFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("unset-rpc-input")
        environment.client.start_flow(flow, flow_id, None)
        environment.client.invoke_rpc(flow.set_note, flow_id, "saved")
        # TypeScript Clients before 1.45.8 leave the input of an input-free RPC unset.
        response = flow_service_client().InvokeRPC(
            pb.InvokeRPCRequest(
                flow_id=flow_id, rpc_name="get_note", request_id=str(uuid4())
            )
        )
        assert response.output.string_value == "saved"
        environment.client.stop_flow(flow_id)
