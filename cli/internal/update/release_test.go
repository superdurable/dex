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

func TestPrintNoticeHighlightsBreakingChangesAndOrdersReleases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, err := writer.Write([]byte(`[
			{"tag_name":"cli-v0.2.0","body":"## Breaking Changes\n\nRemove old command.\n\n## Added\n\nNew command.","draft":false,"prerelease":false},
			{"tag_name":"cli-v0.1.11","body":"## Fixed\n\nFirst update.","draft":false,"prerelease":false}
		]`))
		if err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()
	var output bytes.Buffer
	printNotice(context.Background(), &output, "v0.1.10", &releaseChecker{httpClient: server.Client(), releasesURL: server.URL})
	notice := output.String()
	if strings.Index(notice, "Release notes for cli-v0.1.11") > strings.Index(notice, "Release notes for cli-v0.2.0") {
		t.Fatalf("release notes are not ordered by version: %q", notice)
	}
	if !strings.Contains(notice, ansiBreaking+"## Breaking Changes"+ansiReset) || !strings.Contains(notice, ansiBreaking+"Remove old command."+ansiReset) {
		t.Fatalf("breaking changes are not highlighted: %q", notice)
	}
	if strings.Contains(notice, ansiBreaking+"New command."+ansiReset) || strings.Contains(notice, ansiBreaking+"First update."+ansiReset) {
		t.Fatalf("non-breaking notes are highlighted: %q", notice)
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
