# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Output canaries a scenario loads into a command's launcher and Worker host."""

from __future__ import annotations

from pathlib import Path

CANARY_DIRECTORY_VARIABLE = "DEX_TUI_E2E_CANARY_DIR"
#: Every canary line starts with this text.
CANARY_TEXT = "TUIE2ECANARY"
#: The directory to put on ``PYTHONPATH``; it holds only ``sitecustomize.py``.
PYTHON_CANARY_DIRECTORY = Path(__file__).resolve().parent / "python_path"
NODE_CANARY_SCRIPT = Path(__file__).resolve().parent / "node_canary.cjs"
