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

generate_flow_definition() {
  local source_path="$1"
  local output_path="$2"
  ./cli/dexcli visualize "${source_path}" "${python_arguments[@]}" --out "docs/src/data/flow-definitions/${output_path}"
}

generate_flow_definition examples/python/dex_examples/products/ai-agent/ai_agent_flow.py ai-agent
generate_flow_definition examples/python/dex_examples/products/deal_dsl/deal_dsl_flow.py deal-dsl
generate_flow_definition examples/python/dex_examples/products/job-post/job_post_flow.py job-post
generate_flow_definition examples/python/dex_examples/products/engagement/engagement_flow.py job-seeker-engagement
generate_flow_definition examples/python/dex_examples/products/microservices/orchestration_flow.py microservice-orchestration
generate_flow_definition examples/python/dex_examples/products/money-transfer/money_transfer_flow.py money-transfer
generate_flow_definition examples/python/dex_examples/products/subscription/subscription_flow.py subscription
generate_flow_definition examples/python/dex_examples/products/signup/user_signup_flow.py user-onboarding-process

generate_flow_definition examples/python/dex_examples/products/order-processing/order_processing_flow.py intro/order-processing

generate_flow_definition examples/python/dex_examples/patterns/drain-channels/internal/drain_internal_channels_flow.py design-patterns/drain-internal-channels
generate_flow_definition examples/python/dex_examples/patterns/drain-channels/external_publishing/draining_channel_flow.py design-patterns/draining-external-channel
generate_flow_definition examples/python/dex_examples/patterns/cron/cron_schedule_flow.py design-patterns/cron
generate_flow_definition examples/python/dex_examples/patterns/inactiveness-tracker-timer/inactiveness_tracker_flow.py design-patterns/inactiveness-tracker
generate_flow_definition examples/python/dex_examples/patterns/reminders/reminder_flow.py design-patterns/reminder
generate_flow_definition examples/python/dex_examples/patterns/recovery/failure_recovery_flow.py design-patterns/execute-failure-recovery
generate_flow_definition examples/python/dex_examples/patterns/intervention/manual_recovery_flow.py design-patterns/manual-recovery
generate_flow_definition examples/python/dex_examples/primitives/proceed_on_wait_failure/proceed_on_wait_failure_flow.py design-patterns/wait-for-failure-recovery
generate_flow_definition examples/python/dex_examples/patterns/timeout/flow_graceful_timeout.py design-patterns/graceful-timeout
generate_flow_definition examples/python/dex_examples/patterns/interruptible/interruptible_execution_flow.py design-patterns/interruptible
generate_flow_definition examples/python/dex_examples/patterns/parallel-subflows/submit_request_flow.py design-patterns/parallel-subflows-submit-request
generate_flow_definition examples/python/dex_examples/patterns/parallel-subflows/basic_parent_flow.py design-patterns/parallel-subflows-basic
generate_flow_definition examples/python/dex_examples/patterns/parallel-subflows/advanced_long_live_parent_flow.py design-patterns/parallel-subflows-long-lived
generate_flow_definition examples/python/dex_examples/patterns/parallel-subflows/advanced_short_live_parent_flow.py design-patterns/parallel-subflows-short-lived
generate_flow_definition examples/python/dex_examples/patterns/parallel-subflows/wait_for_half_parent_flow.py design-patterns/parallel-subflows-wait-for-half
generate_flow_definition examples/python/dex_examples/patterns/parallel/await_parallel_steps_flow.py design-patterns/parallel-await
generate_flow_definition examples/python/dex_examples/patterns/parallel/dynamic_parallel_steps_flow.py design-patterns/parallel-dynamic
generate_flow_definition examples/python/dex_examples/patterns/parallel/first_win_parallel_steps_flow.py design-patterns/parallel-first-win
generate_flow_definition examples/python/dex_examples/patterns/parallel/static_parallel_steps_flow.py design-patterns/parallel-static
generate_flow_definition examples/python/dex_examples/patterns/polling/backoff_polling_flow.py design-patterns/polling-backoff
generate_flow_definition examples/python/dex_examples/patterns/polling/iteration_flow.py design-patterns/polling-iteration
generate_flow_definition examples/python/dex_examples/patterns/polling/simple_polling_flow.py design-patterns/polling-timer
