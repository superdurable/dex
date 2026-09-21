// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const dexReleasesURL = "https://api.github.com/repos/superdurable/dex/releases?per_page=100"

type releaseChecker struct {
	httpClient  *http.Client
	releasesURL string
}

type Release struct {
	TagName      string `json:"tag_name"`
	Body         string `json:"body"`
	IsDraft      bool   `json:"draft"`
	IsPrerelease bool   `json:"prerelease"`
}

func newReleaseChecker() *releaseChecker {
	return &releaseChecker{
		httpClient:  &http.Client{Timeout: 2 * time.Second},
		releasesURL: dexReleasesURL,
	}
}

func (checker *releaseChecker) Releases(ctx context.Context) (_ []Release, returnErr error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, checker.releasesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create GitHub releases request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "dexcli")
	response, err := checker.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch GitHub releases: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, response.Body.Close())
	}()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch GitHub releases: unexpected status %s", response.Status)
	}
	var releases []Release
	if err := json.NewDecoder(response.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decode GitHub releases: %w", err)
	}
	return releases, nil
}

func isReleaseVersion(version string) bool {
	_, isValid := releaseVersionParts(version)
	return isValid
}

func isCLIReleaseTag(tagName string) bool {
	return strings.HasPrefix(tagName, "cli-v") && isReleaseVersion(tagName)
}

func isNewerVersion(candidate string, current string) bool {
	candidateParts, candidateIsValid := releaseVersionParts(candidate)
	currentParts, currentIsValid := releaseVersionParts(current)
	if !candidateIsValid || !currentIsValid {
		return false
	}
	for index := range candidateParts {
		if candidateParts[index] != currentParts[index] {
			return candidateParts[index] > currentParts[index]
		}
	}
	return false
}

func releaseVersionParts(version string) ([3]int, bool) {
	version = strings.TrimPrefix(version, "cli-")
	version = strings.TrimPrefix(version, "v")
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var parsed [3]int
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || strconv.Itoa(value) != part {
			return [3]int{}, false
		}
		parsed[index] = value
	}
	return parsed, true
}
