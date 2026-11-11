# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The local end-to-end check of the agent CLI commands and the agent harness examples.

``dex-agents-cli-e2e`` builds each command from a checkout, or installs a
published release, points it at the mock model API, and drives a chat
scenario in a pseudo-terminal against a local ``dexcli dev``. A second suite
runs the agent harness examples through their HTTP session routes. Neither
is part of CI.
"""
