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

"""Slack Connector operations offered to every order support agent as tools.

The Slack manifest exposes one operation to agents: postThreadReply. Each
call runs in its own tool Step with the connector's Call ID, and the agent
asks for approval before it posts. The tokens come from SLACK_BOT_TOKEN and
SLACK_USER_TOKEN when the connector resolves credentials for a call, so they
never enter Flow state.
"""

from __future__ import annotations

import logging
import os
from collections.abc import Mapping

from dex_agents.connectors import ConnectorToolSource, connector_tool
from dex_agents.stepwise import ToolSource
from dex_connectors import slack
from dex_connectors.sdk import Call, ConnectionRef, SecretString

SLACK_CONNECTION_NAME = "support-slack"
SLACK_BOT_TOKEN_VARIABLE = "SLACK_BOT_TOKEN"
SLACK_USER_TOKEN_VARIABLE = "SLACK_USER_TOKEN"

_logger = logging.getLogger(__name__)


def build_slack_tool_sources(connection: slack.Connection | None) -> list[ToolSource]:
    """Returns the Slack tool source, or no source when Slack is not configured."""
    if connection is None:
        return []
    return [
        ConnectorToolSource([
            # The manifest does not ask for approval; this agent asks before every post.
            connector_tool(
                connection.post_thread_reply(), connection.reference, requires_approval=True
            ),
        ]),
    ]


def new_slack_connection_from_environment(
    environment: Mapping[str, str] = os.environ,
) -> slack.Connection | None:
    """Returns a Slack connection when SLACK_BOT_TOKEN is set, otherwise None."""
    if not environment.get(SLACK_BOT_TOKEN_VARIABLE):
        _logger.warning("%s is not set; the Slack connector tools are off", SLACK_BOT_TOKEN_VARIABLE)
        return None
    client = slack.Client(slack.Config(), SlackEnvironmentCredentialProvider(environment))
    return slack.new_connection(client, ConnectionRef("slack", SLACK_CONNECTION_NAME))


class SlackEnvironmentCredentialProvider:
    """Reads the Slack tokens from the environment each time the connector calls Slack."""

    def __init__(self, environment: Mapping[str, str]) -> None:
        self._environment = environment

    async def resolve(self, call: Call) -> slack.Credentials:
        empty = SecretString("")
        return slack.Credentials(
            oauth_client_id="",
            oauth_client_secret=empty,
            bot_token=SecretString(self._environment.get(SLACK_BOT_TOKEN_VARIABLE, "")),
            bot_refresh_token=empty,
            # Optional: confirms a post after an interrupted attempt; without it that post is unknown.
            user_token=SecretString(self._environment.get(SLACK_USER_TOKEN_VARIABLE, "")),
            user_refresh_token=empty,
            app_token=empty,
        )
