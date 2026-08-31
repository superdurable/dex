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

api_url="${DEAL_DSL_API_URL:-http://127.0.0.1:8080}"
api_url="${api_url%/}"
process_id="${DEAL_DSL_PROCESS_ID:-comprehensive-deal-dsl}"
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
test_dir=$(mktemp -d)

cleanup() {
  rm -r "$test_dir"
}
trap cleanup EXIT

for command_name in curl jq; do
  if ! command -v "$command_name" >/dev/null; then
    echo "$command_name is required" >&2
    exit 1
  fi
done

curl --fail --silent --show-error "$api_url/products/deal-dsl/api/actions" >/dev/null

process_payload="$test_dir/process.json"
jq --arg process_id "$process_id" '.processID = $process_id' \
  "$script_dir/products/deal-dsl/ui/deal-dsl/comprehensive-process.json" \
  >"$process_payload"
process_response="$test_dir/process-response.json"
process_status=$(curl --silent --show-error \
  --output "$process_response" \
  --write-out '%{http_code}' \
  "$api_url/products/deal-dsl/api/processes/$process_id")
case "$process_status" in
  200)
    curl --fail --silent --show-error \
      --request PUT \
      -H 'Content-Type: application/json' \
      --data-binary "@$process_payload" \
      "$api_url/products/deal-dsl/api/processes/$process_id" >/dev/null
    ;;
  404)
    curl --fail --silent --show-error \
      -H 'Content-Type: application/json' \
      --data-binary "@$process_payload" \
      "$api_url/products/deal-dsl/api/processes" >/dev/null
    ;;
  *)
    cat "$process_response" >&2
    echo "read Deal DSL process returned HTTP $process_status" >&2
    exit 1
    ;;
esac

curl --fail --silent --show-error "$api_url/products/deal-dsl/api/processes" | \
  jq -e --arg process_id "$process_id" \
    '.processes | any(.processID == $process_id)' >/dev/null

start_execution() {
  local buyer_id="$1"
  curl --fail --silent --show-error \
    -H 'Content-Type: application/json' \
    --data "$(jq -cn --arg process_id "$process_id" --arg buyer_id "$buyer_id" \
      '{processID: $process_id, buyerID: $buyer_id}')" \
    "$api_url/products/deal-dsl/api/executions"
}

send_message() {
  local flow_id="$1"
  local condition_name="$2"
  local data="$3"
  curl --fail --silent --show-error \
    -H 'Content-Type: application/json' \
    --data "$(jq -cn --argjson data "$data" '{data: $data}')" \
    "$api_url/products/deal-dsl/api/executions/${flow_id}/channels/${condition_name}" \
    >/dev/null
}

wait_for_execution() {
  local flow_id="$1"
  local expression="$2"
  local response_file="$test_dir/${flow_id}.json"
  for _ in {1..300}; do
    if curl --fail --silent \
      "$api_url/products/deal-dsl/api/executions/${flow_id}" \
      >"$response_file" && jq -e "$expression" "$response_file" >/dev/null; then
      cat "$response_file"
      return
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
  '{"proposedItemSamplePrice":"10","proposedItemPrice":"100","proposedItemSampleRefund":"5"}'
wait_for_execution "$full_flow" '.pendingPreConditionName == "seller-price-response"' >/dev/null
send_message "$full_flow" seller-price-response '{"acceptedProposedPrice":"false"}'
wait_for_execution "$full_flow" '.currentState == "buyer-negotiation"' >/dev/null
send_message "$full_flow" buyer-proposal \
  '{"proposedItemSamplePrice":"11","proposedItemPrice":"105","proposedItemSampleRefund":"5"}'
wait_for_execution "$full_flow" '.pendingPreConditionName == "seller-price-response"' >/dev/null
send_message "$full_flow" seller-price-response '{"acceptedProposedPrice":"true"}'
wait_for_execution "$full_flow" '.pendingPreConditionName == "item-sample-feedback"' >/dev/null
send_message "$full_flow" item-sample-feedback '{"proceedWithItem":"true"}'
full_result=$(wait_for_execution "$full_flow" \
  '.status == "COMPLETED" and .currentState == "process-item-order" and .stateData.itemDeliveryStatus == "delivered"')

wait_for_execution "$refund_flow" '.currentState == "buyer-negotiation"' >/dev/null
send_message "$refund_flow" buyer-proposal \
  '{"proposedItemSamplePrice":"12","proposedItemPrice":"120","proposedItemSampleRefund":"6"}'
wait_for_execution "$refund_flow" '.pendingPreConditionName == "seller-price-response"' >/dev/null
send_message "$refund_flow" seller-price-response '{"acceptedProposedPrice":"true"}'
wait_for_execution "$refund_flow" '.pendingPreConditionName == "item-sample-feedback"' >/dev/null
send_message "$refund_flow" item-sample-feedback '{"proceedWithItem":"false"}'
refund_result=$(wait_for_execution "$refund_flow" \
  '.status == "COMPLETED" and .currentState == "process-refund" and .stateData.lastAction == "transferMoneyFromSellerToBuyer"')

pending_result=$(wait_for_execution "$pending_flow" \
  '.status == "RUNNING" and .currentState == "buyer-negotiation" and .pendingPreConditionName == ""')
all_executions=$(curl --fail --silent --show-error "$api_url/products/deal-dsl/api/executions")
buyer_executions=$(curl --fail --silent --show-error \
  "$api_url/products/deal-dsl/api/executions?buyerID=${buyer_refund}&processID=${process_id}")
process_executions=$(curl --fail --silent --show-error \
  "$api_url/products/deal-dsl/api/executions?processID=${process_id}")
jq -e --arg full "$full_flow" --arg refund "$refund_flow" --arg pending "$pending_flow" \
  '[.executions[].flowID] | contains([$full, $refund, $pending])' <<<"$all_executions" >/dev/null
jq -e --arg full "$full_flow" --arg refund "$refund_flow" --arg pending "$pending_flow" \
  '[.executions[].flowID] | contains([$full, $refund, $pending])' <<<"$process_executions" >/dev/null
jq -e --arg buyer "$buyer_refund" --arg flow "$refund_flow" \
  '.executions | any(.flowID == $flow and .buyerID == $buyer)' <<<"$buyer_executions" >/dev/null
curl --fail --silent --show-error \
  "$api_url/products/deal-dsl/processes/${process_id}" >/dev/null
curl --fail --silent --show-error \
  "$api_url/products/deal-dsl/executions/${full_flow}" >/dev/null

echo "Deal DSL executions passed"
echo "  process: ${process_id}"
echo "  full:    $(jq -c '{flowID, currentState, status, stateData}' <<<"$full_result")"
echo "  refund:  $(jq -c '{flowID, currentState, status, stateData}' <<<"$refund_result")"
echo "  pending: $(jq -c '{flowID, currentState, status, pendingPreConditionName}' <<<"$pending_result")"
echo "  Deal UI: ${api_url}/products/deal-dsl"
