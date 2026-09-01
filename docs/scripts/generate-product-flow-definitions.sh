#!/usr/bin/env bash

set -euo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repository_root="$(cd "${script_directory}/../.." && pwd)"
python_arguments=()
if [[ -n "${DEX_FLOW_PYTHON:-}" ]]; then
  python_arguments=(--python "${DEX_FLOW_PYTHON}")
fi

cd "${repository_root}"
GOWORK=off go -C cli build -trimpath -o dexcli ./cmd/dexcli

./cli/dexcli visualize examples/python/dex_examples/products/ai-agent/ai_agent_flow.py "${python_arguments[@]}" --out docs/src/data/flow-definitions/ai-agent
./cli/dexcli visualize examples/python/dex_examples/products/deal_dsl/deal_dsl_flow.py "${python_arguments[@]}" --out docs/src/data/flow-definitions/deal-dsl
./cli/dexcli visualize examples/python/dex_examples/products/job-post/job_post_flow.py "${python_arguments[@]}" --out docs/src/data/flow-definitions/job-post
./cli/dexcli visualize examples/python/dex_examples/products/engagement/engagement_flow.py "${python_arguments[@]}" --out docs/src/data/flow-definitions/job-seeker-engagement
./cli/dexcli visualize examples/python/dex_examples/products/microservices/orchestration_flow.py "${python_arguments[@]}" --out docs/src/data/flow-definitions/microservice-orchestration
./cli/dexcli visualize examples/python/dex_examples/products/money-transfer/money_transfer_flow.py "${python_arguments[@]}" --out docs/src/data/flow-definitions/money-transfer
./cli/dexcli visualize examples/python/dex_examples/products/subscription/subscription_flow.py "${python_arguments[@]}" --out docs/src/data/flow-definitions/subscription
./cli/dexcli visualize examples/python/dex_examples/products/signup/user_signup_flow.py "${python_arguments[@]}" --out docs/src/data/flow-definitions/user-onboarding-process
