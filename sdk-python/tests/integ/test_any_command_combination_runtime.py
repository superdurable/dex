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

from dex import FlowErrorType, FlowStatus

from .any_combination_fail_flow import AnyCombinationFailFlow
from .environment import DexDevTestEnvironment
from .shared import unique_id


def test_unknown_condition_id_fails_flow() -> None:
    flow = AnyCombinationFailFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("any-combination-fail")
        run_id = environment.client.start_flow(flow, flow_id, 5)
        failure = environment.client.wait_for_flow(flow_id, timedelta(seconds=30))
        assert failure.status is FlowStatus.FAILED
        assert failure.error_type is FlowErrorType.WORKER_API_FAILED
        assert failure.error_message is not None
        assert "unknown condition ID" in failure.error_message
        info = environment.client.describe_flow(flow_id)
        assert info.run_id == run_id
        assert info.status is FlowStatus.FAILED
