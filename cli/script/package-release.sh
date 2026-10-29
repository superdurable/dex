#!/usr/bin/env bash
# Builds dexcli release archives. Each archive bundles the dex-server-zd binary
# from the server-zd GitHub Release named in cli/SERVER_ZD_RELEASE, so
# `dexcli dev` works right after install.
#
# Usage: script/package-release.sh <version> <output-dir> <os/arch>...
# Example: script/package-release.sh v1.26.0 dist darwin/arm64 linux/amd64
#
# Requires go, gh with read access to the repository, and tar. A missing
# server-zd asset, a checksum mismatch, or diverging license files fail the run.

set -euo pipefail

if (( $# < 3 )); then
  echo "usage: $0 <version> <output-dir> <os/arch>..." >&2
  exit 2
fi
version="$1"
output_directory="$2"
shift 2
targets=("$@")

cli_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repository="${GITHUB_REPOSITORY:-superdurable/dex}"
commit="${GITHUB_SHA:-$(git -C "${cli_directory}" rev-parse HEAD)}"
export GOWORK=off

server_zd_release="$(tr -d '[:space:]' < "${cli_directory}/SERVER_ZD_RELEASE")"
if [[ ! "${server_zd_release}" =~ ^server-zd/v([0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?)$ ]]; then
  echo "cli/SERVER_ZD_RELEASE must name a server-zd/vX.Y.Z tag: ${server_zd_release}" >&2
  exit 1
fi
server_zd_version="${BASH_REMATCH[1]}"

mkdir -p "${output_directory}"
output_directory="$(cd "${output_directory}" && pwd)"
# Downloads stay outside the output directory, whose files are all uploaded.
work_directory="$(mktemp -d)"
trap 'rm -rf "${work_directory}"' EXIT
download_directory="${work_directory}/server-zd"
mkdir -p "${download_directory}"

host_target="$(go env GOHOSTOS)/$(go env GOHOSTARCH)"

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -- "$1" | awk '{ print $1 }'
  else
    shasum -a 256 -- "$1" | awk '{ print $1 }'
  fi
}

gh release download "${server_zd_release}" --repo "${repository}" \
  --pattern checksums.txt --dir "${download_directory}"
server_zd_checksums="${download_directory}/checksums.txt"
[[ -s "${server_zd_checksums}" ]] || {
  echo "${server_zd_release} has no checksums.txt" >&2
  exit 1
}

for target in "${targets[@]}"; do
  os="${target%/*}"
  arch="${target#*/}"
  name="dexcli_${version}_${os}_${arch}"
  package_directory="${work_directory}/package/${name}"
  extract_directory="${work_directory}/extract/${os}_${arch}"
  mkdir -p "${package_directory}" "${extract_directory}"

  server_zd_archive="dex-server-zd_v${server_zd_version}_${os}_${arch}.tar.gz"
  if ! gh release download "${server_zd_release}" --repo "${repository}" \
    --pattern "${server_zd_archive}" --dir "${download_directory}"; then
    echo "${server_zd_release} has no ${server_zd_archive}; refusing to package ${name} without dex-server-zd" >&2
    exit 1
  fi
  [[ -s "${download_directory}/${server_zd_archive}" ]] || {
    echo "${server_zd_release} asset ${server_zd_archive} is empty" >&2
    exit 1
  }
  expected_sha256="$(awk -v archive="${server_zd_archive}" \
    '$2 == archive || $2 == "./" archive { print $1 }' "${server_zd_checksums}")"
  if [[ -z "${expected_sha256}" || "$(wc -l <<< "${expected_sha256}")" -ne 1 ]]; then
    echo "${server_zd_release} checksums.txt must list ${server_zd_archive} exactly once" >&2
    exit 1
  fi
  actual_sha256="$(sha256_of "${download_directory}/${server_zd_archive}")"
  if [[ "${actual_sha256}" != "${expected_sha256}" ]]; then
    echo "${server_zd_archive} SHA-256 ${actual_sha256} does not match checksums.txt ${expected_sha256}" >&2
    exit 1
  fi
  tar -xzf "${download_directory}/${server_zd_archive}" -C "${extract_directory}"
  server_zd_binary="${extract_directory}/dex-server-zd"
  [[ -f "${server_zd_binary}" && ! -L "${server_zd_binary}" && -x "${server_zd_binary}" ]] || {
    echo "${server_zd_archive} does not contain an executable dex-server-zd" >&2
    exit 1
  }
  # dexcli ships one copy of the license files, so the bundled binary's must match.
  if ! cmp -s "${extract_directory}/LICENSE.md" "${cli_directory}/LICENSE" ||
    ! cmp -s "${extract_directory}/LEGACY_NOTICES.md" "${cli_directory}/LEGACY_NOTICES.md" ||
    ! diff -r "${extract_directory}/LICENSES" "${cli_directory}/LICENSES" >/dev/null; then
    echo "${server_zd_archive} license files differ from cli/; ship them before bumping cli/SERVER_ZD_RELEASE" >&2
    exit 1
  fi

  (
    cd "${cli_directory}"
    CGO_ENABLED=0 GOOS="${os}" GOARCH="${arch}" go build -trimpath \
      -ldflags "-s -w -X main.version=${version} -X main.commit=${commit} -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      -o "${package_directory}/dexcli" ./cmd/dexcli
  )
  cp "${server_zd_binary}" "${package_directory}/dex-server-zd"
  chmod 0755 "${package_directory}/dex-server-zd"
  if [[ "${target}" == "${host_target}" ]]; then
    runtime_version="$("${package_directory}/dexcli" version)"
    [[ "${runtime_version}" == "dexcli ${version} "* ]] || {
      echo "dexcli runtime version does not match ${version}: ${runtime_version}" >&2
      exit 1
    }
    server_zd_runtime_version="$("${package_directory}/dex-server-zd" --version)"
    [[ "${server_zd_runtime_version}" == "dex-server-zd ${server_zd_version}" ]] || {
      echo "dex-server-zd runtime version does not match ${server_zd_version}: ${server_zd_runtime_version}" >&2
      exit 1
    }
  fi
  cp "${cli_directory}/LICENSE" "${cli_directory}/LEGACY_NOTICES.md" "${package_directory}/"
  cp -R "${cli_directory}/LICENSES" "${package_directory}/LICENSES"
  tar -C "${package_directory}" -czf "${output_directory}/${name}.tar.gz" \
    dexcli dex-server-zd LICENSE LEGACY_NOTICES.md LICENSES
done

cd "${output_directory}"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum -- ./*.tar.gz > checksums.txt
else
  shasum -a 256 -- ./*.tar.gz > checksums.txt
fi
