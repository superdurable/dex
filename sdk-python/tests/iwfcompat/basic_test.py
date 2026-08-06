# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from datetime import timedelta

from dex import (
    ActiveStepSearchMode,
    Client,
    FlowConfig,
    FlowInfo,
    IdReusePolicy,
    StartFlowOptions,
    StepExecutionId,
    WorkerTarget,
)

from . import iwf_flows
from .shared import ModelInput


def compile_basic_and_reuse(client: Client) -> None:
    options = StartFlowOptions(
        timeout=timedelta(seconds=10),
        id_reuse_policy=IdReusePolicy.ALLOW_IF_NOT_RUNNING,
    )
    client.start_flow(iwf_flows.BASIC, "basic", 10, options)
    output: int = client.wait_for_flow("basic", int)
    client.start_flow(iwf_flows.ABNORMAL_EXIT, "abnormal", 10, options)
    client.start_flow(iwf_flows.BASIC, "abnormal", output, options)


def compile_empty_and_model_inputs(client: Client) -> None:
    client.start_flow(iwf_flows.EMPTY_INPUT, "empty", None)
    client.start_flow(iwf_flows.MODEL_INPUT, "model", ModelInput(value=10))


def compile_failure_policy_and_config_override(client: Client) -> None:
    config = FlowConfig(
        active_step_search_mode=ActiveStepSearchMode.ALL,
        worker_target=WorkerTarget("worker:8803"),
    )
    options = StartFlowOptions(config_override=config)
    client.start_flow(
        iwf_flows.PROCEED_ON_WAIT_FAILURE,
        "recover",
        "input",
        options,
    )
    client.start_flow(iwf_flows.MIXED_WAIT, "mixed", 0, options)
    client.update_flow_config("mixed", config)


def compile_describe_and_step_wait(client: Client) -> None:
    info: FlowInfo = client.describe_flow("basic")
    client.wait_for_step_completion(
        "basic",
        StepExecutionId("BasicSecondStep"),
        timedelta(seconds=5),
    )
    del info
