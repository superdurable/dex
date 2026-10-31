#!/usr/bin/env bash
# Deletes the AWS resources of one benchmark run and verifies that none remains.
# Usage: teardown.sh [instances | server]
set -euo pipefail

run_id="${DEXBENCH_RUN_ID:?set DEXBENCH_RUN_ID}"
profile="${DEXBENCH_AWS_PROFILE:?set DEXBENCH_AWS_PROFILE}"
region="${DEXBENCH_AWS_REGION:-us-west-2}"
state_file="${DEXBENCH_LAUNCH_STATE:-$HOME/.dexbench-$run_id.env}"
name="dex-benchmark-$run_id"
tag_filter="Name=tag:dex-benchmark,Values=$run_id"
if [[ -f "$state_file" ]]; then
  # shellcheck source=/dev/null
  source "$state_file"
fi
bucket="${DEXBENCH_S3_BUCKET:-}"

aws_cli() { aws --profile "$profile" --region "$region" "$@"; }

# live_instances lists the run's instances; an optional role (server or load) narrows it by Name tag.
live_instances() {
  local filters=("$tag_filter" "Name=instance-state-name,Values=pending,running,stopping,stopped,shutting-down")
  if [[ -n "${1:-}" ]]; then filters+=("Name=tag:Name,Values=$name-$1"); fi
  aws_cli ec2 describe-instances --filters "${filters[@]}" --query 'Reservations[].Instances[].InstanceId' --output text
}

terminate_instances() {
  local instances
  instances="$(live_instances "${1:-}")"
  if [[ -n "$instances" ]]; then
    # shellcheck disable=SC2086
    aws_cli ec2 terminate-instances --instance-ids $instances >/dev/null
    # shellcheck disable=SC2086
    aws_cli ec2 wait instance-terminated --instance-ids $instances
  fi
}

delete_everything() {
  terminate_instances
  aws_cli ec2 delete-placement-group --group-name "$name" 2>/dev/null || true
  local group endpoint attempt
  # The server group references the load group, so a second pass finishes the job.
  for attempt in 1 2; do
    for group in $(aws_cli ec2 describe-security-groups --filters "$tag_filter" --query 'SecurityGroups[].GroupId' --output text); do
      aws_cli ec2 delete-security-group --group-id "$group" 2>/dev/null || [[ "$attempt" == 1 ]]
    done
  done
  for endpoint in $(aws_cli ec2 describe-vpc-endpoints --filters "$tag_filter" --query 'VpcEndpoints[].VpcEndpointId' --output text); do
    aws_cli ec2 delete-vpc-endpoints --vpc-endpoint-ids "$endpoint" >/dev/null
  done
  # IAM is not covered by EC2 tag filters, so it is deleted by its run name.
  aws_cli iam remove-role-from-instance-profile --instance-profile-name "$name" --role-name "$name" 2>/dev/null || true
  aws_cli iam delete-instance-profile --instance-profile-name "$name" 2>/dev/null || true
  aws_cli iam delete-role-policy --role-name "$name" --policy-name bucket-access 2>/dev/null || true
  aws_cli iam delete-role --role-name "$name" 2>/dev/null || true
  if [[ -n "$bucket" ]] && bucket_exists; then
    if is_run_bucket; then
      aws_cli s3 rb "s3://$bucket" --force >/dev/null
    else
      echo "bucket $bucket is not tagged dex-benchmark=$run_id; leaving it in place" >&2
    fi
  fi
  rm -f "$state_file"
}

bucket_exists() {
  aws_cli s3api head-bucket --bucket "$bucket" >/dev/null 2>&1
}

# is_run_bucket guards against deleting a bucket of another run with a similar name.
is_run_bucket() {
  [[ "$(aws_cli s3api get-bucket-tagging --bucket "$bucket" --output text \
    --query "TagSet[?Key=='dex-benchmark'].Value | [0]" 2>/dev/null)" == "$run_id" ]]
}

verify_nothing_remains() {
  local remaining=""
  remaining+="$(live_instances)"
  remaining+="$(aws_cli ec2 describe-security-groups --filters "$tag_filter" --query 'SecurityGroups[].GroupId' --output text)"
  remaining+="$(aws_cli ec2 describe-vpc-endpoints --filters "$tag_filter" --query 'VpcEndpoints[].VpcEndpointId' --output text)"
  remaining+="$(aws_cli ec2 describe-placement-groups --filters "$tag_filter" --query 'PlacementGroups[].GroupName' --output text)"
  if [[ -z "$bucket" ]]; then
    echo "no bucket recorded in $state_file or DEXBENCH_S3_BUCKET; check S3 for buckets tagged dex-benchmark=$run_id" >&2
  elif bucket_exists; then
    remaining+=" bucket:$bucket"
  fi
  if aws_cli iam get-role --role-name "$name" >/dev/null 2>&1; then remaining+=" role:$name"; fi
  if aws_cli iam get-instance-profile --instance-profile-name "$name" >/dev/null 2>&1; then remaining+=" instance-profile:$name"; fi
  if [[ -n "${remaining//[[:space:]]/}" ]]; then
    echo "resources remain for $run_id: $remaining" >&2
    exit 1
  fi
  echo "no resources remain for $run_id"
}

case "${1:-all}" in
  instances) terminate_instances; echo "instances remaining: $(live_instances)" ;;
  server) terminate_instances server; echo "server instances remaining: $(live_instances server)" ;;
  all) delete_everything; verify_nothing_remains ;;
  *) echo "usage: teardown.sh [instances | server]" >&2; exit 2 ;;
esac
