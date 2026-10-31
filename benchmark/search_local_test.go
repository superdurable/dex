// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type searchLocalFixture struct{ root string }

func TestSearchLocalCleanupContinuesAfterCommandFailures(t *testing.T) {
	for _, failure := range []string{"logs", "rm", "ps", "gzip", "all"} {
		t.Run(failure, func(t *testing.T) {
			fixture := newSearchLocalFixture(t)
			output, err := fixture.run(failure, false)
			if err == nil {
				t.Fatalf("cleanup failure did not fail runner: %s", output)
			}
			calls := fixture.read(t, "docker-calls.txt")
			for _, step := range []string{"logs", "rm", "ps"} {
				if !strings.Contains(calls, step+"\n") {
					t.Fatalf("cleanup omitted %s: %s", step, calls)
				}
			}
			if fixture.read(t, "gzip-called.txt") != "called\n" {
				t.Fatal("gzip was not attempted")
			}
			if _, statErr := os.Stat(filepath.Join(fixture.root, "run-data")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("run directory remains: %v", statErr)
			}
			failures := fixture.read(t, "out/cleanup-errors.txt")
			if failure == "all" {
				for _, step := range []string{"docker logs", "docker rm", "docker ps", "gzip"} {
					if !strings.Contains(failures, step+" failed") {
						t.Fatalf("missing failure record for %s: %s", step, failures)
					}
				}
			} else if !strings.Contains(failures, failure+" failed") {
				t.Fatalf("missing failure record: %s", failures)
			}
		})
	}
}

func TestSearchLocalReadinessTimeoutStopsBeforeBenchmark(t *testing.T) {
	fixture := newSearchLocalFixture(t)
	output, err := fixture.run("none", true)
	if err == nil || !strings.Contains(string(output), "did not become ready after 60 attempts") {
		t.Fatalf("readiness timeout not returned: %v %s", err, output)
	}
	calls := fixture.read(t, "docker-calls.txt")
	if strings.Count(calls, "readiness\n") != 60 {
		t.Fatalf("readiness attempts: %s", calls)
	}
	if strings.Contains(calls, "image\n") {
		t.Fatalf("runner continued past readiness timeout: %s", calls)
	}
	if _, statErr := os.Stat(filepath.Join(fixture.root, "run-data")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("timeout left data directory: %v", statErr)
	}
}

func newSearchLocalFixture(t *testing.T) *searchLocalFixture {
	t.Helper()
	fixture := &searchLocalFixture{root: t.TempDir()}
	source, err := os.ReadFile("search-local.sh")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"benchmark/search-local.sh": string(source),
		"benchmark/bin/dexbench": `#!/usr/bin/env bash
if [[ "$1" == wait-ready ]]; then
  for attempt in {1..1000}; do
    if [[ -f "$FIXTURE_ROOT/server-started" ]]; then exit 0; fi
    /bin/sleep 0.01
  done
  exit 9
fi
exit 0
`,
		"server-zd/target/release/dex-server-zd": `#!/usr/bin/env bash
if [[ "$1" == --version ]]; then printf 'fixture\n'; exit 0; fi
printf 'fixture server log\n'
printf 'started\n' > "$FIXTURE_ROOT/server-started"
`,
		"bin/docker": `#!/usr/bin/env bash
printf '%s\n' "$1" >> "$FIXTURE_ROOT/docker-calls.txt"
case "$1" in
  run) printf 'fixture-container\n';;
  port) printf '127.0.0.1:15432\n';;
  exec)
    if [[ "$3" == pg_isready ]]; then
      printf 'readiness\n' >> "$FIXTURE_ROOT/docker-calls.txt"
      if [[ "$READY_FAIL" == true ]]; then exit 1; fi
    fi;;
  logs|rm|ps)
    if [[ "$FAIL_CLEANUP" == "$1" || "$FAIL_CLEANUP" == all ]]; then exit 42; fi;;
esac
`,
		"bin/gzip": `#!/usr/bin/env bash
printf 'called\n' > "$FIXTURE_ROOT/gzip-called.txt"
if [[ "$FAIL_CLEANUP" == gzip || "$FAIL_CLEANUP" == all ]]; then exit 43; fi
exit 0
`,
		"bin/sleep": "#!/usr/bin/env bash\nexit 0\n",
		"bin/mktemp": `#!/usr/bin/env bash
mkdir "$FIXTURE_ROOT/run-data"
printf '%s\n' "$FIXTURE_ROOT/run-data"
`,
	}
	for name, content := range files {
		path := filepath.Join(fixture.root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func (fixture *searchLocalFixture) run(failure string, shouldFailReadiness bool) ([]byte, error) {
	command := exec.Command("bash", filepath.Join(fixture.root, "benchmark/search-local.sh"), "postgres", "100", "0", filepath.Join(fixture.root, "out"))
	readiness := "false"
	if shouldFailReadiness {
		readiness = "true"
	}
	command.Env = append(os.Environ(), "FIXTURE_ROOT="+fixture.root, "FAIL_CLEANUP="+failure, "READY_FAIL="+readiness, "PATH="+filepath.Join(fixture.root, "bin")+":"+os.Getenv("PATH"))
	return command.CombinedOutput()
}

func (fixture *searchLocalFixture) read(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(fixture.root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
