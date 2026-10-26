#!/usr/bin/env bash
# Deletes every resource create.sh made for <name>, then lists anything still tagged. Safe to rerun.
# Usage: destroy.sh <region> <name>
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"

instances="$(aws ec2 describe-instances --filters "$tag_filter" Name=instance-state-name,Values=pending,running,stopping,stopped,shutting-down \
  --query 'Reservations[].Instances[].InstanceId' --output text)"
if [[ -n "$instances" ]]; then
  # shellcheck disable=SC2086 # Instance IDs are whitespace-separated by design.
  aws ec2 terminate-instances --instance-ids $instances >/dev/null
  # shellcheck disable=SC2086
  aws ec2 wait instance-terminated --instance-ids $instances
fi
for group in $(aws ec2 describe-security-groups --filters "$tag_filter" --query 'SecurityGroups[].GroupId' --output text); do
  aws ec2 delete-security-group --group-id "$group" || echo "security group $group still in use; rerun later" >&2
done
for volume in $(aws ec2 describe-volumes --filters "$tag_filter" Name=status,Values=available --query 'Volumes[].VolumeId' --output text); do
  aws ec2 delete-volume --volume-id "$volume"
done

if aws iam get-instance-profile --instance-profile-name "$profile_name" >/dev/null 2>&1; then
  for role in $(aws iam get-instance-profile --instance-profile-name "$profile_name" --query 'InstanceProfile.Roles[].RoleName' --output text); do
    aws iam remove-role-from-instance-profile --instance-profile-name "$profile_name" --role-name "$role"
  done
  aws iam delete-instance-profile --instance-profile-name "$profile_name"
fi
if aws iam get-role --role-name "$role_name" >/dev/null 2>&1; then
  for policy in $(aws iam list-attached-role-policies --role-name "$role_name" --query 'AttachedPolicies[].PolicyArn' --output text); do
    aws iam detach-role-policy --role-name "$role_name" --policy-arn "$policy"
  done
  for policy in $(aws iam list-role-policies --role-name "$role_name" --query 'PolicyNames' --output text); do
    aws iam delete-role-policy --role-name "$role_name" --policy-name "$policy"
  done
  aws iam delete-role --role-name "$role_name"
fi

for bucket in $(tagged_buckets); do
  if aws s3api head-bucket --bucket "$bucket" >/dev/null 2>&1; then
    aws s3 rm "s3://$bucket" --recursive --only-show-errors
    aws s3api delete-bucket --bucket "$bucket"
  fi
done

"$(dirname "${BASH_SOURCE[0]}")/leftovers.sh" "$region" "$name"
