#!/usr/bin/env bash
# Creates the benchmark S3 bucket, a bucket-scoped instance role and profile, and one SSM-managed EC2 host.
# Every resource is tagged dex-benchmark=<name>. Usage: create.sh <region> <name>
set -euo pipefail
# shellcheck source=common.sh
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
instance_type="${DEXBENCH_INSTANCE_TYPE:-m6id.2xlarge}"
root_volume_gb="${DEXBENCH_ROOT_VOLUME_GB:-50}"

bucket="$(tagged_buckets | head -1)"
if [[ -n "$bucket" ]]; then
  require_owned bucket "$bucket"
else
  bucket="$name-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
  if [[ "$region" == us-east-1 ]]; then
    aws s3api create-bucket --bucket "$bucket" >/dev/null
  else
    aws s3api create-bucket --bucket "$bucket" --create-bucket-configuration "LocationConstraint=$region" >/dev/null
  fi
  aws s3api put-public-access-block --bucket "$bucket" \
    --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
  aws s3api put-bucket-tagging --bucket "$bucket" --tagging "TagSet=[{Key=$tag_key,Value=$name}]"
fi
echo "bucket: $bucket"

trust_policy='{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}'
if aws iam get-role --role-name "$role_name" >/dev/null 2>&1; then
  require_owned role "$role_name"
else
  aws iam create-role --role-name "$role_name" --assume-role-policy-document "$trust_policy" \
    --tags "Key=$tag_key,Value=$name" >/dev/null
fi
bucket_policy="$(printf '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:ListBucket","s3:GetBucketLocation"],"Resource":"arn:aws:s3:::%s"},{"Effect":"Allow","Action":["s3:GetObject","s3:PutObject","s3:DeleteObject","s3:AbortMultipartUpload","s3:ListMultipartUploadParts"],"Resource":"arn:aws:s3:::%s/*"}]}' "$bucket" "$bucket")"
aws iam put-role-policy --role-name "$role_name" --policy-name "$policy_name" --policy-document "$bucket_policy"
aws iam attach-role-policy --role-name "$role_name" --policy-arn arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore
if aws iam get-instance-profile --instance-profile-name "$profile_name" >/dev/null 2>&1; then
  require_owned profile "$profile_name"
else
  aws iam create-instance-profile --instance-profile-name "$profile_name" --tags "Key=$tag_key,Value=$name" >/dev/null
fi
if [[ -z "$(aws iam get-instance-profile --instance-profile-name "$profile_name" --query 'InstanceProfile.Roles[].RoleName' --output text)" ]]; then
  aws iam add-role-to-instance-profile --instance-profile-name "$profile_name" --role-name "$role_name"
fi

vpc_id="$(aws ec2 describe-vpcs --filters Name=isDefault,Values=true --query 'Vpcs[0].VpcId' --output text)"
if [[ "$vpc_id" == None || -z "$vpc_id" ]]; then echo "no default VPC in $region" >&2; exit 1; fi
security_group="$(aws ec2 describe-security-groups --filters "$tag_filter" Name=vpc-id,Values="$vpc_id" --query 'SecurityGroups[0].GroupId' --output text)"
if [[ "$security_group" == None || -z "$security_group" ]]; then
  # No ingress rules: the host is reached only through SSM Session Manager.
  security_group="$(aws ec2 create-security-group --group-name "$name" --description "Dex benchmark host, no ingress" \
    --vpc-id "$vpc_id" --tag-specifications "ResourceType=security-group,Tags=[{Key=$tag_key,Value=$name}]" \
    --query GroupId --output text)"
fi

existing="$(aws ec2 describe-instances --filters "$tag_filter" Name=instance-state-name,Values=pending,running,stopping,stopped \
  --query 'Reservations[].Instances[].InstanceId' --output text)"
if [[ -n "$existing" ]]; then
  echo "instance: $existing (already exists)"
  echo "bucket=$bucket"
  exit 0
fi
image_id="$(aws ssm get-parameter --name /aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id \
  --query Parameter.Value --output text)"
tags="{Key=$tag_key,Value=$name},{Key=Name,Value=$name}"
instance_id=""
launch_errors="$(mktemp)"
trap 'rm -f "$launch_errors"' EXIT
# A new instance profile can take a few seconds to become usable by EC2.
for attempt in 1 2 3 4 5 6 7 8 9 10; do
  if instance_id="$(aws ec2 run-instances --image-id "$image_id" --instance-type "$instance_type" \
    --iam-instance-profile "Name=$profile_name" --security-group-ids "$security_group" \
    --metadata-options HttpTokens=required,HttpEndpoint=enabled \
    --block-device-mappings "DeviceName=/dev/sda1,Ebs={VolumeSize=$root_volume_gb,VolumeType=gp3,DeleteOnTermination=true}" \
    --tag-specifications "ResourceType=instance,Tags=[$tags]" "ResourceType=volume,Tags=[$tags]" \
    --query 'Instances[0].InstanceId' --output text 2>"$launch_errors")"; then
    break
  fi
  echo "run-instances attempt $attempt failed: $(tail -1 "$launch_errors")" >&2
  instance_id=""
  sleep 10
done
if [[ -z "$instance_id" ]]; then echo "could not launch the instance" >&2; exit 1; fi
aws ec2 wait instance-running --instance-ids "$instance_id"
echo "instance: $instance_id"
echo "bucket=$bucket"
echo "wait for SSM: aws ssm describe-instance-information --filters Key=InstanceIds,Values=$instance_id"
echo "connect:      aws ssm start-session --region $region --target $instance_id"
