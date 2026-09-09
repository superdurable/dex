#!/bin/sh
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

set -eu

for rules in AGENTS.md CLAUDE.md .cursor/rules/project-core.mdc; do
  grep -Fq 'Dex Developer Skill Routing' "$rules"
  grep -Fq 'examples/**' "$rules"
  grep -Fq 'pure Server, IDL, or SDK implementation work' "$rules"
  grep -Fq 'docs.superdurable.io/build-with-ai/dex-developer-skill' "$rules"
done

grep -Fq '$dex-developer' AGENTS.md
grep -Fq '/dex:dex-developer' CLAUDE.md
grep -Fq '`dex-developer` skill' .cursor/rules/project-core.mdc
