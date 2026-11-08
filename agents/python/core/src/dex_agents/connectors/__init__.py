# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Connector operations as Stepwise agent tools.

Install the extra ``dex-agents[connectors]``, which brings in the Connector
SDK. Name each reviewed operation an agent may use with ``connector_tool`` and
put them in a ``ConnectorToolSource``. The tool name, description, argument
schema, effect class, and approval default come from the operation's
``dex-connector.json``.

Example:
    >>> from dex_agents.connectors import ConnectorToolSource, connector_tool
    >>> refund = connector_tool(stripe.operations.create_refund(), stripe.reference)
    >>> agent = AgentDefinition(
    ...     name="billing",
    ...     instructions="You handle refunds.",
    ...     model=model,
    ...     tool_sources=[ConnectorToolSource([refund])],
    ... )
"""

from dex_agents.connectors._source import (
    CONNECTOR_MANIFEST_FILE_NAME,
    CONNECTOR_NOT_SENT_RETRY_LATER,
    CONNECTOR_RESULT_TRUNCATED_NOTE,
    DEFAULT_CONNECTOR_RESULT_MAX_BYTES,
    DEFAULT_CONNECTOR_SOURCE_NAME,
    ApprovalPredicate,
    ConnectorBranchOutcome,
    ConnectorTool,
    ConnectorToolSource,
    bound_connector_result,
    classify_connector_branch,
    connector_tool,
    format_connector_result,
)
from dex_agents.connectors._step_context import ConnectorStepContext

__all__ = [
    "CONNECTOR_MANIFEST_FILE_NAME",
    "CONNECTOR_NOT_SENT_RETRY_LATER",
    "CONNECTOR_RESULT_TRUNCATED_NOTE",
    "DEFAULT_CONNECTOR_RESULT_MAX_BYTES",
    "DEFAULT_CONNECTOR_SOURCE_NAME",
    "ApprovalPredicate",
    "ConnectorBranchOutcome",
    "ConnectorStepContext",
    "ConnectorTool",
    "ConnectorToolSource",
    "bound_connector_result",
    "classify_connector_branch",
    "connector_tool",
    "format_connector_result",
]
