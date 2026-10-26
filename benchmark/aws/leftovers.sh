#!/usr/bin/env bash
# Lists resources still tagged dex-benchmark=<name>, plus its IAM role and instance profile.
# Exits 1 when anything remains. Usage: leftovers.sh <region> <name>
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"

remaining=0
# The tagging API can report a deleted resource for a few minutes, so confirm live state too.
for arn in $(tagged_resources | grep -Ev ':(instance|volume)/' || true); do
  echo "tagged: $arn"
  remaining=1
done
for instance in $(aws ec2 describe-instances --filters "$tag_filter" Name=instance-state-name,Values=pending,running,stopping,stopped,shutting-down \
  --query 'Reservations[].Instances[].InstanceId' --output text); do
  echo "instance: $instance"
  remaining=1
done
for volume in $(aws ec2 describe-volumes --filters "$tag_filter" --query 'Volumes[].VolumeId' --output text); do
  echo "volume: $volume"
  remaining=1
done
if aws iam get-role --role-name "$role_name" >/dev/null 2>&1; then echo "IAM role: $role_name"; remaining=1; fi
if aws iam get-instance-profile --instance-profile-name "$profile_name" >/dev/null 2>&1; then echo "IAM instance profile: $profile_name"; remaining=1; fi
if [[ "$remaining" == 0 ]]; then echo "no dex-benchmark=$name resources remain in $region"; fi
exit "$remaining"
