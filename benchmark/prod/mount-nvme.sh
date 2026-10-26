#!/usr/bin/env bash
# Formats and mounts instance-store NVMe, and moves Docker's data root onto it.
set -euo pipefail

mount_point="${DEXBENCH_NVME_MOUNT:-/mnt/nvme}"
if findmnt -rno TARGET "$mount_point" >/dev/null 2>&1; then
  echo "$mount_point is already mounted"
else
  # EBS volumes also appear as nvme* on Nitro instances; only the instance-store model is eligible.
  disk="$(lsblk -dpno NAME,MODEL | awk '/Instance Storage/ {print $1; exit}')"
  if [[ -z "$disk" ]]; then echo "no instance-store NVMe disk found" >&2; exit 1; fi
  if [[ -n "$(lsblk -no MOUNTPOINT "$disk" | tr -d '[:space:]')" ]]; then echo "$disk is already in use" >&2; exit 1; fi
  sudo mkfs.ext4 -F -E nodiscard -L dexbench "$disk"
  sudo mkdir -p "$mount_point"
  sudo mount -o noatime "$disk" "$mount_point"
fi
sudo mkdir -p "$mount_point/work" "$mount_point/docker"
sudo chown "$(id -un):$(id -gn)" "$mount_point/work"
sudo mkdir -p /etc/docker
printf '{\n  "data-root": "%s/docker"\n}\n' "$mount_point" | sudo tee /etc/docker/daemon.json >/dev/null
if systemctl is-active --quiet docker 2>/dev/null; then sudo systemctl restart docker; fi
lsblk -o NAME,MODEL,SIZE,MOUNTPOINT
echo "export DEXBENCH_NVME_DIR=$mount_point/work"
