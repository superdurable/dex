#!/usr/bin/env bash
# Shared inputs for the benchmark AWS scripts. Source it; do not run it.
# Usage: <script> <region> <name>. The name becomes the dex-benchmark tag value and the IAM name prefix.
# shellcheck disable=SC2034 # The sourcing scripts use these names.

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <region> <name>" >&2
  exit 2
fi
region="$1"
name="$2"
if [[ ! "$name" =~ ^[a-z0-9][a-z0-9-]{2,40}$ ]]; then
  echo "name must be 3-41 lowercase letters, digits, or hyphens" >&2
  exit 2
fi
export AWS_REGION="$region" AWS_DEFAULT_REGION="$region" AWS_PAGER=""
tag_key="dex-benchmark"
role_name="$name-instance"
profile_name="$name-instance"
policy_name="$name-bucket-access"
tag_filter="Name=tag:$tag_key,Values=$name"

tagged_resources() {
  aws resourcegroupstaggingapi get-resources --tag-filters "Key=$tag_key,Values=$name" \
    --query 'ResourceTagMappingList[].ResourceARN' --output text | tr '\t' '\n' | sed '/^$/d'
}

tagged_buckets() {
  tagged_resources | sed -n 's#^arn:aws:s3:::##p'
}

# has_benchmark_tag reads a tag list as "key<TAB>value" lines and succeeds for dex-benchmark=<name>.
has_benchmark_tag() {
  awk -F '\t' -v key="$tag_key" -v value="$name" '$1 == key && $2 == value {found = 1} END {exit !found}'
}

# require_owned refuses to touch an existing resource unless it carries dex-benchmark=<name>.
# Usage: require_owned role|profile|bucket <resource name>
require_owned() {
  local kind="$1" resource="$2" tags
  case "$kind" in
    role) tags="$(aws iam list-role-tags --role-name "$resource" --query 'Tags[].[Key,Value]' --output text)" ;;
    profile) tags="$(aws iam list-instance-profile-tags --instance-profile-name "$resource" --query 'Tags[].[Key,Value]' --output text)" ;;
    bucket) tags="$(aws s3api get-bucket-tagging --bucket "$resource" --query 'TagSet[].[Key,Value]' --output text 2>/dev/null || true)" ;;
    *) echo "unknown resource kind $kind" >&2; exit 2 ;;
  esac
  if ! has_benchmark_tag <<< "$tags"; then
    echo "refusing to modify $kind $resource: it is not tagged $tag_key=$name" >&2
    exit 1
  fi
}
