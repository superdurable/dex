#!/bin/bash

# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included in
# all copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
# THE SOFTWARE.

set -euo pipefail

postgres_port="${DATASET_DEAL_POSTGRES_PORT:-15432}"
dex_port="${DATASET_DEAL_DEX_PORT:-20801}"
dex_web_port="${DATASET_DEAL_DEX_WEB_PORT:-20802}"
worker_port="${DATASET_DEAL_WORKER_PORT:-20803}"
api_port="${DATASET_DEAL_API_PORT:-20804}"
temporal_port="${DATASET_DEAL_TEMPORAL_PORT:-20233}"
temporal_ui_port="${DATASET_DEAL_TEMPORAL_UI_PORT:-20333}"
dex_address="127.0.0.1:${dex_port}"
temporal_address="127.0.0.1:${temporal_port}"
api_address="127.0.0.1:${api_port}"
postgres_url="postgres://dataset_deal:dataset_deal@127.0.0.1:${postgres_port}/dataset_deal?sslmode=disable"
compose_project="dataset-deal-demo-$$"
test_dir=$(mktemp -d)
dex_log="/tmp/dataset-deal-dex.log"
app_log="/tmp/dataset-deal-app.log"
dexcli_pid=""
app_pid=""

cleanup() {
  if [[ -n "$app_pid" ]] && kill -0 "$app_pid" 2>/dev/null; then
    kill -TERM "$app_pid"
    wait "$app_pid" || true
  fi
  if [[ -n "$dexcli_pid" ]] && kill -0 "$dexcli_pid" 2>/dev/null; then
    kill -TERM "$dexcli_pid"
    wait "$dexcli_pid" || true
  fi
  DATASET_DEAL_POSTGRES_PORT="$postgres_port" docker compose \
    -p "$compose_project" \
    -f dataset-deal/docker-compose.yml \
    down --volumes >/dev/null 2>&1 || true
  rm -r "$test_dir"
}
trap cleanup EXIT

for command_name in docker temporal curl jq; do
  if ! command -v "$command_name" >/dev/null; then
    echo "$command_name is required" >&2
    exit 1
  fi
done

DATASET_DEAL_POSTGRES_PORT="$postgres_port" docker compose \
  -p "$compose_project" \
  -f dataset-deal/docker-compose.yml \
  up -d --wait

make -C ../../cli build
make bins
: >"$dex_log"
: >"$app_log"
../../cli/dexcli dev \
  -bind-address 127.0.0.1 \
  -dex-port "$dex_port" \
  -web-port "$dex_web_port" \
  -temporal-port "$temporal_port" \
  -temporal-ui-port "$temporal_ui_port" \
  -temporal-db-filename "$test_dir/temporal.db" \
  >>"$dex_log" 2>&1 &
dexcli_pid=$!

temporal_ready=false
for _ in {1..240}; do
  if temporal --address "$temporal_address" operator search-attribute list >/dev/null 2>&1; then
    temporal_ready=true
    break
  fi
  if ! kill -0 "$dexcli_pid" 2>/dev/null; then
    cat "$dex_log" >&2
    echo "dexcli exited before Temporal became ready" >&2
    exit 1
  fi
  sleep 0.25
done
if ! $temporal_ready; then
  cat "$dex_log" >&2
  echo "Temporal did not become ready" >&2
  exit 1
fi

./dataset-deal/register-search-attributes.sh "$temporal_address"

DATASET_DEAL_POSTGRES_URL="$postgres_url" \
DEX_FLOW_SERVICE_ADDRESS="$dex_address" \
DEX_WORKER_BIND_ADDRESS="127.0.0.1:${worker_port}" \
DEX_WORKER_TARGET="127.0.0.1:${worker_port}" \
DEX_EXAMPLES_HTTP_ADDRESS="$api_address" \
DEX_BLOB_CACHE_DIR="$test_dir/blob-cache" \
  ./dex-samples >>"$app_log" 2>&1 &
app_pid=$!

api_ready=false
for _ in {1..240}; do
  if curl --fail --silent "http://${api_address}/api/dataset-deal/actions" >/dev/null; then
    api_ready=true
    break
  fi
  if ! kill -0 "$app_pid" 2>/dev/null; then
    cat "$app_log" >&2
    echo "Go examples server exited before becoming ready" >&2
    exit 1
  fi
  sleep 0.25
done
if ! $api_ready; then
  cat "$app_log" >&2
  echo "Go examples API did not become ready" >&2
  exit 1
fi

process_id="comprehensive-dataset-deal"
process_payload="$test_dir/process.json"
jq --arg process_id "$process_id" '.processID = $process_id' \
  cmd/server/dex/ui/dataset-deal/comprehensive-process.json >"$process_payload"
curl --fail --silent --show-error \
  -H 'Content-Type: application/json' \
  --data-binary "@$process_payload" \
  "http://${api_address}/api/dataset-deal/processes" >/dev/null
curl --fail --silent --show-error \
  "http://${api_address}/api/dataset-deal/processes" | \
  jq -e --arg process_id "$process_id" \
    '.processes | any(.processID == $process_id)' >/dev/null

tables=$(DATASET_DEAL_POSTGRES_PORT="$postgres_port" docker compose \
  -p "$compose_project" \
  -f dataset-deal/docker-compose.yml \
  exec -T postgres psql -U dataset_deal -d dataset_deal -Atc \
  "SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename")
if [[ "$tables" != "dataset_deal_processes" ]]; then
  echo "unexpected Dataset Deal tables: ${tables}" >&2
  exit 1
fi

start_execution() {
  local buyer_id="$1"
  curl --fail --silent --show-error \
    -H 'Content-Type: application/json' \
    --data "$(jq -cn --arg process_id "$process_id" --arg buyer_id "$buyer_id" \
      '{processID: $process_id, buyerID: $buyer_id}')" \
    "http://${api_address}/api/dataset-deal/executions"
}

send_message() {
  local flow_id="$1"
  local condition_name="$2"
  local data="$3"
  curl --fail --silent --show-error \
    -H 'Content-Type: application/json' \
    --data "$(jq -cn --argjson data "$data" '{data: $data}')" \
    "http://${api_address}/api/dataset-deal/executions/${flow_id}/channels/${condition_name}" \
    >/dev/null
}

wait_for_execution() {
  local flow_id="$1"
  local expression="$2"
  local response_file="$test_dir/${flow_id}.json"
  for _ in {1..300}; do
    if curl --fail --silent \
      "http://${api_address}/api/dataset-deal/executions/${flow_id}" \
      >"$response_file" && jq -e "$expression" "$response_file" >/dev/null; then
      cat "$response_file"
      return
    fi
    if ! kill -0 "$app_pid" 2>/dev/null; then
      cat "$app_log" >&2
      echo "Go examples server exited while waiting for ${flow_id}" >&2
      exit 1
    fi
    sleep 0.1
  done
  cat "$response_file" >&2 || true
  echo "execution ${flow_id} did not satisfy ${expression}" >&2
  exit 1
}

buyer_full="buyer1"
buyer_refund="buyer2"
buyer_pending="buyer3"
full_flow=$(start_execution "$buyer_full" | jq -r '.flowID')
refund_flow=$(start_execution "$buyer_refund" | jq -r '.flowID')
pending_flow=$(start_execution "$buyer_pending" | jq -r '.flowID')

wait_for_execution "$full_flow" '.currentState == "buyer-negotiation"' >/dev/null
send_message "$full_flow" buyer-proposal \
  '{"proposedSamplePrice":"10","proposedFullPrice":"100","proposedSampleRefundPrice":"5"}'
wait_for_execution "$full_flow" '.pendingPreConditionName == "seller-price-response"' >/dev/null
send_message "$full_flow" seller-price-response '{"acceptedProposedPrice":"false"}'
wait_for_execution "$full_flow" '.pendingPreConditionName == "seller-price-response"' >/dev/null
send_message "$full_flow" seller-price-response '{"acceptedProposedPrice":"true"}'
wait_for_execution "$full_flow" '.pendingPreConditionName == "sample-feedback"' >/dev/null
send_message "$full_flow" sample-feedback '{"proceedToFullDataset":"true"}'
full_result=$(wait_for_execution "$full_flow" \
  '.status == "COMPLETED" and .currentState == "process-full-order" and .stateData.deliveredDataset == "full"')

wait_for_execution "$refund_flow" '.currentState == "buyer-negotiation"' >/dev/null
send_message "$refund_flow" buyer-proposal \
  '{"proposedSamplePrice":"12","proposedFullPrice":"120","proposedSampleRefundPrice":"6"}'
wait_for_execution "$refund_flow" '.pendingPreConditionName == "seller-price-response"' >/dev/null
send_message "$refund_flow" seller-price-response '{"acceptedProposedPrice":"true"}'
wait_for_execution "$refund_flow" '.pendingPreConditionName == "sample-feedback"' >/dev/null
send_message "$refund_flow" sample-feedback '{"proceedToFullDataset":"false"}'
refund_result=$(wait_for_execution "$refund_flow" \
  '.status == "COMPLETED" and .currentState == "process-refund" and .stateData.lastAction == "transferMoneyFromSellerToBuyer"')

pending_result=$(wait_for_execution "$pending_flow" \
  '.status == "RUNNING" and .currentState == "buyer-negotiation" and .pendingPreConditionName == ""')
all_executions=$(curl --fail --silent --show-error "http://${api_address}/api/dataset-deal/executions")
buyer_executions=$(curl --fail --silent --show-error \
  "http://${api_address}/api/dataset-deal/executions?buyerID=${buyer_refund}&processID=${process_id}")
process_executions=$(curl --fail --silent --show-error \
  "http://${api_address}/api/dataset-deal/executions?processID=${process_id}")
jq -e --arg full "$full_flow" --arg refund "$refund_flow" --arg pending "$pending_flow" \
  '[.executions[].flowID] | contains([$full, $refund, $pending])' <<<"$all_executions" >/dev/null
jq -e --arg full "$full_flow" --arg refund "$refund_flow" --arg pending "$pending_flow" \
  '[.executions[].flowID] | contains([$full, $refund, $pending])' <<<"$process_executions" >/dev/null
jq -e --arg buyer "$buyer_refund" \
  '.executions | length == 1 and .[0].buyerID == $buyer' <<<"$buyer_executions" >/dev/null
curl --fail --silent --show-error \
  "http://${api_address}/dataset-deal/processes/${process_id}" >/dev/null
curl --fail --silent --show-error \
  "http://${api_address}/dataset-deal/executions/${full_flow}" >/dev/null

echo "Dataset Deal DSL E2E passed"
echo "  process: ${process_id}"
echo "  full:    $(jq -c '{flowID, currentState, status, stateData}' <<<"$full_result")"
echo "  refund:  $(jq -c '{flowID, currentState, status, stateData}' <<<"$refund_result")"
echo "  pending: $(jq -c '{flowID, currentState, status, pendingPreConditionName}' <<<"$pending_result")"
echo "  Deal UI: http://${api_address}/dataset-deal"
echo "  Dex UI:  http://127.0.0.1:${dex_web_port}"
echo "  Temporal: http://127.0.0.1:${temporal_ui_port}"
echo "  logs:     ${dex_log}, ${app_log}"
if [[ "${KEEP_DATASET_DEAL_DEMO:-0}" == "1" ]]; then
  echo "Services remain running. Press Ctrl+C to stop and clean up."
  wait "$app_pid"
fi
