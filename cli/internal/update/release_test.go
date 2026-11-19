// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package update

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestReleaseCheckerReturnsPublishedCLIReleases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/releases" {
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
		if _, err := writer.Write([]byte(`[
			{"tag_name":"cli-v0.1.11","body":"old","draft":false,"prerelease":false},
			{"tag_name":"cli-v0.1.12","body":"draft","draft":true,"prerelease":false},
			{"tag_name":"cli-v0.2.0","body":"preview","draft":false,"prerelease":true},
			{"tag_name":"server/v0.3.0","body":"server","draft":false,"prerelease":false},
			{"tag_name":"cli-v0.1.13","body":"latest","draft":false,"prerelease":false}
		]`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()
	checker := &releaseChecker{httpClient: server.Client(), releasesURL: server.URL + "/releases"}
	releases, err := checker.Releases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	newerReleases := releasesNewerThan(releases, "v0.1.10")
	if len(newerReleases) != 2 || newerReleases[0].TagName != "cli-v0.1.11" || newerReleases[1].TagName != "cli-v0.1.13" {
		t.Fatalf("newer releases = %#v", newerReleases)
	}
}

// fixtureReleasesJSON mirrors published dexcli release bodies, including a stray section after the change-log link.
const fixtureReleasesJSON = `[
	{"tag_name":"cli-v0.2.1","published_at":"2026-11-19T10:33:44Z","draft":false,"prerelease":false,
	 "body":"## Breaking Changes\n\nNone.\n\n## Fixed\n\n- **dexcli visualize** lists enum values in the same order on every run, even when the constants are declared across several files of one package. Since 0.2.1.\n\nFull change log: https://docs.example.test/cli/changelog#v0-2\n"},
	{"tag_name":"server/v0.3.0","published_at":"2026-11-18T10:00:00Z","draft":false,"prerelease":false,"body":"server"},
	{"tag_name":"cli-v0.2.0","published_at":"2026-11-18T07:20:56Z","draft":false,"prerelease":false,
	 "body":"## Breaking Changes\n\n- Remove the old command.\n\n## Added\n\n- New command.\n\nFull change log: https://docs.example.test/cli/changelog#v0-2\n\n\n### Changed\n\n- Late change."},
	{"tag_name":"cli-v0.1.11","published_at":"2026-11-17T04:42:56Z","draft":false,"prerelease":false,
	 "body":"## Breaking Changes\n\nNone.\n\n## Fixed\n\n- First update.\n\nFull change log: https://docs.example.test/cli/changelog#v0-1\n"},
	{"tag_name":"cli-v0.1.10","published_at":"2026-11-16T00:00:00Z","draft":false,"prerelease":false,"body":"## Fixed\n\n- Current."}
]`

func newFixtureReleaseChecker(t *testing.T) *releaseChecker {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, err := writer.Write([]byte(fixtureReleasesJSON)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return &releaseChecker{httpClient: server.Client(), releasesURL: server.URL + "/releases?per_page=100"}
}

func TestPrintNoticeIsShortAndPointsToReleaseNotes(t *testing.T) {
	var output bytes.Buffer
	printNotice(context.Background(), &output, "v0.1.10", newFixtureReleaseChecker(t))
	notice := output.String()
	lines := strings.Split(strings.TrimSpace(notice), "\n")
	if len(lines) != 3 {
		t.Fatalf("notice has %d lines, want 3:\n%s", len(lines), notice)
	}
	if !strings.Contains(lines[0], "cli-v0.2.1 (you have v0.1.10)") {
		t.Fatalf("notice does not name the versions: %q", lines[0])
	}
	expected := "What's new since v0.1.10 (3 releases, " + ansiBreaking + "includes breaking changes" + ansiReset + "): run dexcli release-notes"
	if lines[2] != expected {
		t.Fatalf("notice summary = %q, want %q", lines[2], expected)
	}
	if strings.Contains(notice, "First update.") || strings.Contains(notice, "Full change log") {
		t.Fatalf("notice includes release notes: %q", notice)
	}
}

func TestPrintNoticeOmitsBreakingChangesWhenNone(t *testing.T) {
	var output bytes.Buffer
	printNotice(context.Background(), &output, "v0.2.0", newFixtureReleaseChecker(t))
	if !strings.HasSuffix(output.String(), "What's new since v0.2.0 (1 release): run dexcli release-notes\n") {
		t.Fatalf("notice = %q", output.String())
	}
}

func TestReleaseNotesRenderNewestFirstWithoutEmptySections(t *testing.T) {
	var output bytes.Buffer
	if err := executeReleaseNotes(context.Background(), nil, &output, "v0.1.10", false, newFixtureReleaseChecker(t)); err != nil {
		t.Fatal(err)
	}
	expected := `dexcli release notes since v0.1.10, through v0.2.1 (3 releases)

v0.2.1  (2026-11-19)
  Fixed
  - dexcli visualize lists enum values in the same order on every run, even when the constants are
    declared across several files of one package. Since 0.2.1.

v0.2.0  (2026-11-18)
  Breaking Changes
  - Remove the old command.
  Added
  - New command.
  Changed
  - Late change.

v0.1.11  (2026-11-17)
  Fixed
  - First update.

Full change log: https://docs.example.test/cli/changelog#v0-2

Full change log: https://docs.example.test/cli/changelog#v0-1
`
	if output.String() != expected {
		t.Fatalf("release notes =\n%s\nwant\n%s", output.String(), expected)
	}
	for _, line := range strings.Split(output.String(), "\n") {
		if len(line) > releaseNotesWidth {
			t.Fatalf("line is wider than %d columns: %q", releaseNotesWidth, line)
		}
	}
}

func TestReleaseNotesColorsOnlyForTerminal(t *testing.T) {
	var output bytes.Buffer
	if err := executeReleaseNotes(context.Background(), []string{"cli-v0.1.11"}, &output, "v0.1.10", true, newFixtureReleaseChecker(t)); err != nil {
		t.Fatal(err)
	}
	notes := output.String()
	if !strings.Contains(notes, ansiBreaking+"Breaking Changes"+ansiReset) || !strings.Contains(notes, ansiBreaking+"Remove the old command."+ansiReset) {
		t.Fatalf("breaking changes are not highlighted: %q", notes)
	}
	if !strings.Contains(notes, ansiBold+"dexcli visualize"+ansiReset) {
		t.Fatalf("bold markup is not rendered: %q", notes)
	}
	if strings.Contains(notes, "First update.") || strings.Contains(notes, "**") {
		t.Fatalf("release notes include the baseline release or raw markup: %q", notes)
	}
}

func TestReleaseNotesReportsUpToDateAndRejectsBadVersions(t *testing.T) {
	var output bytes.Buffer
	if err := executeReleaseNotes(context.Background(), nil, &output, "v0.2.1", false, newFixtureReleaseChecker(t)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "No dexcli release is newer than v0.2.1.") {
		t.Fatalf("up-to-date output = %q", output.String())
	}
	output.Reset()
	if err := executeReleaseNotes(context.Background(), nil, &output, "dev", false, newFixtureReleaseChecker(t)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "dexcli release notes for v0.2.1\n") || strings.Contains(output.String(), "v0.2.0  (") {
		t.Fatalf("development build output = %q", output.String())
	}
	if err := executeReleaseNotes(context.Background(), []string{"latest"}, &output, "v0.1.10", false, newFixtureReleaseChecker(t)); err == nil {
		t.Fatal("an invalid version did not fail")
	}
}

func TestReleasesAfterPagesUntilTheBaselineRelease(t *testing.T) {
	var requestedPages []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		page := request.URL.Query().Get("page")
		requestedPages = append(requestedPages, page)
		releases := make([]string, 0, releasesPageSize)
		for index := 0; index < releasesPageSize; index++ {
			releases = append(releases, `{"tag_name":"server/v0.1.`+strconv.Itoa(index)+`"}`)
		}
		if page == "2" {
			releases[0] = `{"tag_name":"cli-v0.1.10"}`
		}
		if _, err := writer.Write([]byte("[" + strings.Join(releases, ",") + "]")); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	checker := &releaseChecker{httpClient: server.Client(), releasesURL: server.URL + "/releases?per_page=100"}
	releases, isComplete, err := checker.ReleasesAfter(context.Background(), "v0.1.10", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 2*releasesPageSize || strings.Join(requestedPages, ",") != "1,2" || !isComplete {
		t.Fatalf("read %d releases from pages %q, complete %t", len(releases), requestedPages, isComplete)
	}
}

// newEndlessReleasesChecker serves full pages that never reach the running version; page 1 has two dexcli releases.
func newEndlessReleasesChecker(t *testing.T) (*releaseChecker, *[]string) {
	t.Helper()
	requestedPages := &[]string{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		page := request.URL.Query().Get("page")
		*requestedPages = append(*requestedPages, page)
		releases := make([]string, 0, releasesPageSize)
		for index := 0; index < releasesPageSize; index++ {
			releases = append(releases, `{"tag_name":"server/v0.1.`+strconv.Itoa(index)+`"}`)
		}
		if page == "" || page == "1" {
			releases[0] = `{"tag_name":"cli-v0.2.1","body":"## Fixed\n\n- Newest."}`
			releases[1] = `{"tag_name":"cli-v0.2.0","body":"## Fixed\n\n- Older."}`
		}
		if _, err := writer.Write([]byte("[" + strings.Join(releases, ",") + "]")); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return &releaseChecker{httpClient: server.Client(), releasesURL: server.URL + "/releases?per_page=100"}, requestedPages
}

func TestPrintNoticeSaysWhenTheReleaseListIsTruncated(t *testing.T) {
	checker, _ := newEndlessReleasesChecker(t)
	var output bytes.Buffer
	printNotice(context.Background(), &output, "v0.1.10", checker)
	if !strings.HasSuffix(output.String(), "What's new since v0.1.10 (2+ releases): run dexcli release-notes\n") {
		t.Fatalf("notice = %q", output.String())
	}
}

func TestReleaseNotesSaysWhenOlderReleasesAreOmitted(t *testing.T) {
	checker, requestedPages := newEndlessReleasesChecker(t)
	var output bytes.Buffer
	if err := executeReleaseNotes(context.Background(), nil, &output, "v0.1.10", false, checker); err != nil {
		t.Fatal(err)
	}
	if len(*requestedPages) != releaseNotesMaxPages ||
		!strings.HasSuffix(output.String(), "\nOlder releases omitted: see "+cliChangeLogURL+"\n") ||
		!strings.Contains(output.String(), "- Newest.") {
		t.Fatalf("pages %q, release notes:\n%s", *requestedPages, output.String())
	}
}

func TestPrintNoticeSkipsDevelopmentVersionsAndReleaseFailures(t *testing.T) {
	var output bytes.Buffer
	printNotice(context.Background(), &output, "dev", newReleaseChecker())
	if output.Len() != 0 {
		t.Fatalf("development version output = %q", output.String())
	}
	noUpdateServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, err := writer.Write([]byte(`[{"tag_name":"cli-v0.1.0","draft":false,"prerelease":false}]`))
		if err != nil {
			t.Fatal(err)
		}
	}))
	defer noUpdateServer.Close()
	printNotice(context.Background(), &output, "v0.1.0", &releaseChecker{httpClient: noUpdateServer.Client(), releasesURL: noUpdateServer.URL})
	if output.Len() != 0 {
		t.Fatalf("no-update output = %q", output.String())
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	printNotice(context.Background(), &output, "v0.1.0", &releaseChecker{httpClient: server.Client(), releasesURL: server.URL})
	if output.Len() != 0 {
		t.Fatalf("failure output = %q", output.String())
	}
}

func TestIsNewerVersion(t *testing.T) {
	testCases := []struct {
		name      string
		candidate string
		current   string
		isNewer   bool
	}{
		{name: "patch", candidate: "cli-v0.1.13", current: "v0.1.12", isNewer: true},
		{name: "minor", candidate: "cli-v0.2.0", current: "v0.1.12", isNewer: true},
		{name: "same", candidate: "cli-v0.1.12", current: "v0.1.12", isNewer: false},
		{name: "older", candidate: "cli-v0.1.11", current: "v0.1.12", isNewer: false},
		{name: "invalid", candidate: "cli-vnext", current: "v0.1.12", isNewer: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			isNewer := isNewerVersion(testCase.candidate, testCase.current)
			if isNewer != testCase.isNewer {
				t.Fatalf("isNewerVersion(%q, %q) = %t, want %t", testCase.candidate, testCase.current, isNewer, testCase.isNewer)
			}
		})
	}
}
