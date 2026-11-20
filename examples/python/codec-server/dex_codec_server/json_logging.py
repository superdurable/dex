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

"""Writes one JSON object per log line."""

from __future__ import annotations

import json
import logging
from datetime import datetime, timezone
from typing import Any

LOG_FIELDS_ATTRIBUTE = "log_fields"


class JsonLogFormatter(logging.Formatter):
    """Format records as JSON.

    It drops exception tracebacks: their text may hold payloads or key provider errors.
    """

    def format(self, record: logging.LogRecord) -> str:
        """Return the record's time, level, logger, message, and structured fields."""
        line: dict[str, Any] = {
            "time": datetime.fromtimestamp(record.created, timezone.utc).isoformat(),
            "level": record.levelname,
            "logger": record.name,
            "message": record.getMessage(),
        }
        fields = getattr(record, LOG_FIELDS_ATTRIBUTE, None)
        if isinstance(fields, dict):
            line.update(fields)
        return json.dumps(line, default=str)


def configure_json_logging(level: int = logging.INFO) -> None:
    """Send every logger, including aiohttp's and the SDK's, through ``JsonLogFormatter``."""
    handler = logging.StreamHandler()
    handler.setFormatter(JsonLogFormatter())
    root = logging.getLogger()
    root.handlers[:] = [handler]
    root.setLevel(level)
