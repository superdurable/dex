# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Builds the embedded Dex Web module and compiles a host module against it as a consumer would.

The host module requires the packaged version with no replace directive. A file:// module proxy serves the
package; every other module, including the released sdk-go, comes from the regular Go module proxy.
"""

from __future__ import annotations

import argparse
import os
import subprocess
import sys
import tempfile
import zipfile
from pathlib import Path

from package_embedded_web_module import (
    EMBEDDED_MODULE,
    PackagingError,
    UnreleasedSdkGoError,
    build_module,
    newest_sdk_go_version,
)

CHECK_VERSION = "v1.999.0"
HOST_GO_MOD = f"""module example.com/host

go 1.26.0

require {EMBEDDED_MODULE} {CHECK_VERSION}
"""
HOST_TEST = """package host

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/web/embedded"
)

func TestHostServesEmbeddedDexWeb(t *testing.T) {
	handler, err := embedded.NewDexWebHandler(&embedded.Config{
		ResolveTarget: func(request *http.Request) (embedded.Target, error) {
			if request.PathValue("serverID") != "server-a" {
				return embedded.Target{}, embedded.ErrTargetNotFound
			}
			return embedded.Target{FlowServiceClient: dexpb.NewFlowServiceClient(nil), BasePath: "/dex-web/server-a"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/dex-web/{serverID}/", handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	page := get(t, server.URL+"/dex-web/server-a/v1/flows")
	script := regexp.MustCompile(`src="(/dex-web/server-a/assets/[^"]+\\.js)"`).FindStringSubmatch(page)
	if len(script) != 2 || !strings.Contains(page, `content="/dex-web/server-a"`) {
		t.Fatalf("page does not load the SPA under the base path: %s", page)
	}
	if asset := get(t, server.URL+script[1]); len(asset) == 0 {
		t.Fatal("SPA script is empty")
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, %v", url, response.StatusCode, err)
	}
	return string(body)
}
"""


def write_module_proxy(module_directory: Path, proxy_directory: Path) -> None:
    version_directory = proxy_directory / EMBEDDED_MODULE / "@v"
    version_directory.mkdir(parents=True)
    (version_directory / "list").write_text(CHECK_VERSION + "\n")
    (version_directory / f"{CHECK_VERSION}.info").write_text(f'{{"Version":"{CHECK_VERSION}"}}\n')
    (version_directory / f"{CHECK_VERSION}.mod").write_bytes((module_directory / "go.mod").read_bytes())
    prefix = f"{EMBEDDED_MODULE}@{CHECK_VERSION}/"
    with zipfile.ZipFile(version_directory / f"{CHECK_VERSION}.zip", "w", zipfile.ZIP_DEFLATED) as archive:
        for file in sorted(module_directory.rglob("*")):
            if file.is_file():
                archive.write(file, prefix + file.relative_to(module_directory).as_posix())


def check_consumer(work_directory: Path, sdk_go_version: str) -> None:
    module_directory = work_directory / "module"
    build_module(module_directory, sdk_go_version)
    proxy_directory = work_directory / "proxy"
    write_module_proxy(module_directory, proxy_directory)
    host_directory = work_directory / "host"
    host_directory.mkdir()
    (host_directory / "go.mod").write_text(HOST_GO_MOD)
    (host_directory / "host_test.go").write_text(HOST_TEST)
    upstream_proxy = os.environ.get("GOPROXY", "https://proxy.golang.org,direct")
    environment = {
        **os.environ,
        "GOWORK": "off",
        "GOFLAGS": "-mod=mod",
        "GOPROXY": f"{proxy_directory.as_uri()},{upstream_proxy}",
        "GONOSUMDB": EMBEDDED_MODULE,
        "GOPRIVATE": "",
    }
    for command in (["go", "mod", "tidy"], ["go", "test", "./..."]):
        result = subprocess.run(command, cwd=host_directory, env=environment, text=True, capture_output=True)
        if result.returncode != 0:
            raise PackagingError(f"{' '.join(command)} failed in the host module:\n{result.stdout}{result.stderr}")
    if "replace" in (host_directory / "go.mod").read_text():
        raise PackagingError("The host module must not need a replace directive")


def main() -> int:
    parser = argparse.ArgumentParser(description="Compile a host module against the packaged embedded Dex Web")
    parser.add_argument("--sdk-go-version", default="", help="released sdk-go version; default newest sdk-go/v* tag")
    parser.add_argument(
        "--skip-if-sdk-go-unreleased",
        action="store_true",
        help="exit 0 when web needs sdk-go changes that are not released yet, as on a protocol pull request",
    )
    arguments = parser.parse_args()
    try:
        sdk_go_version = arguments.sdk_go_version or newest_sdk_go_version()
        with tempfile.TemporaryDirectory(prefix="dex-web-embedded-") as work_directory:
            check_consumer(Path(work_directory), sdk_go_version)
    except UnreleasedSdkGoError as error:
        if not arguments.skip_if_sdk_go_unreleased:
            print(error, file=sys.stderr)
            return 1
        print(f"Skipped: {error}")
        return 0
    except PackagingError as error:
        print(error, file=sys.stderr)
        return 1
    print(f"A host module built and served {EMBEDDED_MODULE} with sdk-go {sdk_go_version}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
