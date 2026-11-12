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

"""The Agent Quick Start: one small order support agent for each framework.

Each framework file has two functions. build_plain_agent is the agent as the
framework alone writes it. build_durable_agent wraps the same agent in a Dex
agent session and gives each tool a policy. The __main__ module runs one
session from the command line.
"""
