// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package dev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReleaseCheckerFindsLatestPublishedCLIRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/releases" {
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
		if _, err := writer.Write([]byte(`[
			{"tag_name":"cli-v0.1.11","draft":false,"prerelease":false},
			{"tag_name":"cli-v0.1.12","draft":true,"prerelease":false},
			{"tag_name":"cli-v0.2.0","draft":false,"prerelease":true},
			{"tag_name":"server/v0.3.0","draft":false,"prerelease":false},
			{"tag_name":"cli-v0.1.13","draft":false,"prerelease":false}
		]`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()
	checker := &releaseChecker{httpClient: server.Client(), releasesURL: server.URL + "/releases"}
	latestVersion, err := checker.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if latestVersion != "cli-v0.1.13" {
		t.Fatalf("latest version = %q, want cli-v0.1.13", latestVersion)
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
