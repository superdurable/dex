#!/usr/bin/env bash
# Creates the tagged AWS resources of one benchmark run.
# Usage: launch.sh infra | server | load
set -euo pipefail

run_id="${DEXBENCH_RUN_ID:?set DEXBENCH_RUN_ID, such as 2026-10-30-s3}"
profile="${DEXBENCH_AWS_PROFILE:?set DEXBENCH_AWS_PROFILE}"
region="${DEXBENCH_AWS_REGION:-us-west-2}"
zone="${DEXBENCH_AZ:-${region}a}"
key_name="${DEXBENCH_KEY_NAME:-}"
shutdown_minutes="${DEXBENCH_SHUTDOWN_MINUTES:-150}"
state_file="${DEXBENCH_LAUNCH_STATE:-$HOME/.dexbench-$run_id.env}"
name="dex-benchmark-$run_id"
tag="Key=dex-benchmark,Value=$run_id"

aws_cli() { aws --profile "$profile" --region "$region" "$@"; }
tag_spec() { echo "ResourceType=$1,Tags=[{$tag},{Key=Name,Value=$name-${2:-$1}}]"; }
save() { echo "$1=$2" >> "$state_file"; echo "$1=$2"; }

create_infra() {
  [[ ! -f "$state_file" ]] || { echo "$state_file exists; run teardown.sh first" >&2; exit 1; }
  local vpc subnet bucket route_tables endpoint existing_endpoint my_cidr load_group server_group
  vpc="$(aws_cli ec2 describe-vpcs --filters Name=is-default,Values=true --query 'Vpcs[0].VpcId' --output text)"
  subnet="$(aws_cli ec2 describe-subnets --filters "Name=vpc-id,Values=$vpc" "Name=availability-zone,Values=$zone" \
    Name=default-for-az,Values=true --query 'Subnets[0].SubnetId' --output text)"
  save DEXBENCH_VPC "$vpc"
  save DEXBENCH_SUBNET "$subnet"

  bucket="$name-$(openssl rand -hex 4)"
  aws_cli s3api create-bucket --bucket "$bucket" --create-bucket-configuration "LocationConstraint=$region" >/dev/null
  aws_cli s3api put-bucket-tagging --bucket "$bucket" --tagging "TagSet=[{$tag}]"
  aws_cli s3api put-bucket-lifecycle-configuration --bucket "$bucket" --lifecycle-configuration \
    '{"Rules":[{"ID":"expire-after-one-day","Status":"Enabled","Filter":{},"Expiration":{"Days":1},"AbortIncompleteMultipartUpload":{"DaysAfterInitiation":1}}]}'
  save DEXBENCH_S3_BUCKET "$bucket"

  # Reuse an existing S3 gateway endpoint and leave it in place.
  existing_endpoint="$(aws_cli ec2 describe-vpc-endpoints --filters "Name=vpc-id,Values=$vpc" \
    "Name=service-name,Values=com.amazonaws.$region.s3" Name=vpc-endpoint-type,Values=Gateway \
    --query 'VpcEndpoints[0].VpcEndpointId' --output text)"
  if [[ "$existing_endpoint" == None ]]; then
    route_tables="$(aws_cli ec2 describe-route-tables --filters "Name=vpc-id,Values=$vpc" --query 'RouteTables[].RouteTableId' --output text)"
    # shellcheck disable=SC2086
    endpoint="$(aws_cli ec2 create-vpc-endpoint --vpc-id "$vpc" --service-name "com.amazonaws.$region.s3" \
      --vpc-endpoint-type Gateway --route-table-ids $route_tables --tag-specifications "$(tag_spec vpc-endpoint)" \
      --query VpcEndpoint.VpcEndpointId --output text)"
    save DEXBENCH_S3_ENDPOINT_ID "$endpoint"
  else
    echo "reusing S3 gateway endpoint $existing_endpoint"
  fi

  aws_cli iam create-role --role-name "$name" --tags "$tag" --assume-role-policy-document \
    '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}' >/dev/null
  aws_cli iam put-role-policy --role-name "$name" --policy-name bucket-access --policy-document \
    "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Action\":\"s3:*\",\"Resource\":[\"arn:aws:s3:::$bucket\",\"arn:aws:s3:::$bucket/*\"]}]}"
  aws_cli iam create-instance-profile --instance-profile-name "$name" --tags "$tag" >/dev/null
  aws_cli iam add-role-to-instance-profile --instance-profile-name "$name" --role-name "$name"

  my_cidr="${DEXBENCH_SSH_CIDR:-$(curl -fsS https://checkip.amazonaws.com | tr -d '[:space:]')/32}"
  load_group="$(aws_cli ec2 create-security-group --group-name "$name-load" --description "dexbench load host" \
    --vpc-id "$vpc" --tag-specifications "$(tag_spec security-group load)" --query GroupId --output text)"
  server_group="$(aws_cli ec2 create-security-group --group-name "$name-server" --description "dexbench server host" \
    --vpc-id "$vpc" --tag-specifications "$(tag_spec security-group server)" --query GroupId --output text)"
  aws_cli ec2 authorize-security-group-ingress --group-id "$load_group" --protocol tcp --port 22 --cidr "$my_cidr" >/dev/null
  aws_cli ec2 authorize-security-group-ingress --group-id "$server_group" --protocol tcp --port 22 --cidr "$my_cidr" >/dev/null
  # The load host reaches the server over SSH, the API port, and the metrics port.
  aws_cli ec2 authorize-security-group-ingress --group-id "$server_group" --ip-permissions \
    "IpProtocol=tcp,FromPort=22,ToPort=22,UserIdGroupPairs=[{GroupId=$load_group}]" \
    "IpProtocol=tcp,FromPort=8801,ToPort=8801,UserIdGroupPairs=[{GroupId=$load_group}]" \
    "IpProtocol=tcp,FromPort=9901,ToPort=9901,UserIdGroupPairs=[{GroupId=$load_group}]" >/dev/null
  save DEXBENCH_LOAD_GROUP "$load_group"
  save DEXBENCH_SERVER_GROUP "$server_group"

  aws_cli ec2 create-placement-group --group-name "$name" --strategy cluster --tag-specifications "$(tag_spec placement-group)"
  save DEXBENCH_PLACEMENT_GROUP "$name"
}

launch_instance() {
  local role="$1" instance_type group profile_argument=() user_data ami instance_id
  # shellcheck source=/dev/null
  source "$state_file"
  if [[ "$role" == server ]]; then
    instance_type="${DEXBENCH_SERVER_INSTANCE_TYPE:-c7gd.large}"
    group="$DEXBENCH_SERVER_GROUP"
    profile_argument=(--iam-instance-profile "Name=$name")
  else
    instance_type="${DEXBENCH_LOAD_INSTANCE_TYPE:-c7g.4xlarge}"
    group="$DEXBENCH_LOAD_GROUP"
  fi
  user_data="$(mktemp)"
  {
    echo '#!/bin/bash'
    echo "shutdown -h +$shutdown_minutes"
    echo 'dnf install -y sysstat'
    if [[ "$role" == server ]]; then
      # Pick the instance store by model name; device numbering is not stable.
      echo "device=\$(lsblk -dpno NAME,MODEL | awk '/Instance Storage/ {print \$1; exit}')"
      # shellcheck disable=SC2016
      echo 'mkfs.xfs -f "$device" && mkdir -p /mnt/nvme && mount "$device" /mnt/nvme && chown ec2-user: /mnt/nvme'
    fi
  } > "$user_data"
  ami="$(aws_cli ssm get-parameter --name /aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-arm64 \
    --query Parameter.Value --output text)"
  instance_id="$(aws_cli ec2 run-instances --image-id "$ami" --instance-type "$instance_type" \
    ${key_name:+--key-name "$key_name"} --subnet-id "$DEXBENCH_SUBNET" --security-group-ids "$group" \
    ${profile_argument[@]+"${profile_argument[@]}"} --placement "GroupName=$DEXBENCH_PLACEMENT_GROUP" \
    --instance-market-options 'MarketType=spot,SpotOptions={SpotInstanceType=one-time,InstanceInterruptionBehavior=terminate}' \
    --instance-initiated-shutdown-behavior terminate --metadata-options HttpTokens=required \
    --block-device-mappings 'DeviceName=/dev/xvda,Ebs={VolumeSize=20,VolumeType=gp3,DeleteOnTermination=true}' \
    --user-data "file://$user_data" \
    --tag-specifications "$(tag_spec instance "$role")" "$(tag_spec volume "$role")" "$(tag_spec spot-instances-request "$role")" \
    --query 'Instances[0].InstanceId' --output text)"
  rm -f "$user_data"
  aws_cli ec2 wait instance-running --instance-ids "$instance_id"
  aws_cli ec2 describe-instances --instance-ids "$instance_id" --output text \
    --query 'Reservations[0].Instances[0].[InstanceId,InstanceType,Placement.AvailabilityZone,PublicIpAddress,PrivateIpAddress]'
}

case "${1:-}" in
  infra) create_infra ;;
  server | load) launch_instance "$1" ;;
  *) echo "usage: launch.sh infra|server|load" >&2; exit 2 ;;
esac
