# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""PTY end-to-end support for the agent CLI commands with the dexcli terminal UI; never published.

``terminal_screen`` renders what a command draws, ``terminal_session`` drives
one command on a pseudo-terminal, and ``chat_run`` holds one scenario's home,
workspace, scripted model, and sessions. The scenarios are in ``scenarios/``.
"""
