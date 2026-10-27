#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 ]]; then
  echo "host-setup requires Linux x86_64" >&2
  exit 1
fi

if [[ ! -f /etc/docker/daemon.json ]]; then
  echo "run benchmark/prod/mount-nvme.sh first so Docker data lands on instance-store NVMe" >&2
  exit 1
fi

sudo apt-get update
sudo apt-get install -y ca-certificates curl gnupg jq git python3 python3-yaml tar xz-utils tmux
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --batch --yes --dearmor -o /etc/apt/keyrings/docker.gpg
sudo chmod a+r /etc/apt/keyrings/docker.gpg
ubuntu_codename="$(awk -F= '$1 == "VERSION_CODENAME" {gsub(/"/, "", $2); print $2}' /etc/os-release)"
echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $ubuntu_codename stable" | sudo tee /etc/apt/sources.list.d/docker.list >/dev/null
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo usermod -aG docker "$(id -un)"

go_archive="$(curl -fsSL 'https://go.dev/dl/?mode=json' | jq -r '.[0].files[] | select(.os=="linux" and .arch=="amd64" and .kind=="archive") | .filename' | head -1)"
if [[ -z "$go_archive" ]]; then echo "could not find current Go archive" >&2; exit 1; fi
curl -fsSL "https://go.dev/dl/$go_archive" -o "/tmp/$go_archive"
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf "/tmp/$go_archive"
sudo ln -sfn /usr/local/go/bin/go /usr/local/bin/go

curl -fsSL https://temporal.download/cli.sh -o /tmp/temporal-cli-install.sh
bash /tmp/temporal-cli-install.sh
if [[ -f "$HOME/.temporalio/bin/temporal" ]]; then
  sudo ln -sfn "$HOME/.temporalio/bin/temporal" /usr/local/bin/temporal
fi

release_tag() {
  local prefix="$1"
  curl -fsSL 'https://api.github.com/repos/superdurable/dex/releases?per_page=100' |
    jq -r --arg prefix "$prefix" '.[] | select(.draft == false and .prerelease == false and (.tag_name | startswith($prefix))) | .tag_name' |
    sort -V | tail -1
}

wait_for_load() {
  local load
  while :; do
    load="$(uptime | sed -E 's/.*load averages?: ([0-9.]+).*/\1/')"
    if awk -v current_load="$load" -v limit="${DEXBENCH_MAX_LOAD:-$(getconf _NPROCESSORS_ONLN)}" 'BEGIN { exit !(current_load < limit) }'; then return; fi
    sleep 15
  done
}

install_root="$HOME/.local/dex-benchmark"
mkdir -p "$install_root/bin"

download_release_binary() {
  local tag="$1" pattern="$2" destination="$3"
  local metadata asset_url archive
  metadata="$(curl -fsSL "https://api.github.com/repos/superdurable/dex/releases/tags/$tag")"
  asset_url="$(jq -r --arg pattern "$pattern" '.assets[] | select(.name | test($pattern)) | .browser_download_url' <<< "$metadata" | head -1)"
  if [[ -z "$asset_url" ]]; then return 1; fi
  archive="$(mktemp)"
  curl -fsSL "$asset_url" -o "$archive"
  archive_dir="$(mktemp -d)"
  tar -xzf "$archive" -C "$archive_dir"
  binary_path="$(find "$archive_dir" -type f -name "$destination" -print -quit)"
  if [[ -z "$binary_path" ]]; then echo "$destination missing from $tag asset" >&2; return 1; fi
  install -m 0755 "$binary_path" "$install_root/bin/$destination"
  rm -rf "$archive_dir"
  rm -f "$archive"
}

zd_tag="$(release_tag 'server-zd/v')"
server_tag="$(release_tag 'server/v')"
if [[ -z "$zd_tag" || -z "$server_tag" ]]; then echo "missing server release tag" >&2; exit 1; fi
download_release_binary "$zd_tag" 'linux_amd64[.]tar[.]gz$' dex-server-zd
if ! download_release_binary "$server_tag" 'linux_amd64[.]tar[.]gz$' dex-server; then
  echo "$server_tag has no Linux binary asset; building its tagged source" >&2
  source_dir="$(mktemp -d)"
  git clone --depth 1 --branch "$server_tag" https://github.com/superdurable/dex.git "$source_dir"
  node_archive="$(curl -fsSL https://nodejs.org/dist/latest-v22.x/SHASUMS256.txt | awk '$2 ~ /linux-x64.tar.xz$/ {print $2; exit}')"
  curl -fsSL "https://nodejs.org/dist/latest-v22.x/$node_archive" -o "/tmp/$node_archive"
  mkdir -p "$install_root/node"
  tar -xJf "/tmp/$node_archive" -C "$install_root/node" --strip-components=1
  export PATH="$install_root/node/bin:$PATH"
  wait_for_load
  (cd "$source_dir/web" && nice -n 10 npm ci)
  wait_for_load
  (cd "$source_dir/web" && nice -n 10 npm run build)
  wait_for_load
  (cd "$source_dir/server" && nice -n 10 go build -o "$install_root/bin/dex-server" ./cmd/server)
  rm -rf "$source_dir"
fi
printf 'server-zd=%s\nserver=%s\n' "$zd_tag" "$server_tag" > "$install_root/releases.txt"
curl -fsSL "https://raw.githubusercontent.com/superdurable/dex/$server_tag/server/config/development.yaml" -o "$install_root/server-development.yaml"
echo "Installed Go, Temporal CLI, Docker Compose, and Dex server binaries in $install_root/bin"
