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
	"net/url"
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
	PublishedAt  string `json:"published_at"`
}

// releasesPageSize matches per_page in dexReleasesURL; a shorter page is the last one.
const releasesPageSize = 100

func newReleaseChecker() *releaseChecker {
	return &releaseChecker{
		httpClient:  &http.Client{Timeout: 2 * time.Second},
		releasesURL: dexReleasesURL,
	}
}

func (checker *releaseChecker) Releases(ctx context.Context) ([]Release, error) {
	return checker.releasesPage(ctx, checker.releasesURL)
}

// ReleasesAfter returns releases page by page until a page reaches a dexcli release not newer than baselineVersion.
// It reads at most maxPages pages, since the list holds the releases of every Dex component.
// The bool is false when maxPages ended the reading, so older releases after baselineVersion may be missing.
func (checker *releaseChecker) ReleasesAfter(ctx context.Context, baselineVersion string, maxPages int) ([]Release, bool, error) {
	pageURL, err := url.Parse(checker.releasesURL)
	if err != nil {
		return nil, false, fmt.Errorf("parse GitHub releases URL: %w", err)
	}
	var releases []Release
	for page := 1; page <= maxPages; page++ {
		query := pageURL.Query()
		query.Set("page", strconv.Itoa(page))
		pageURL.RawQuery = query.Encode()
		pageReleases, err := checker.releasesPage(ctx, pageURL.String())
		if err != nil {
			return nil, false, err
		}
		releases = append(releases, pageReleases...)
		if isLastNeededReleasesPage(pageReleases, baselineVersion) {
			return releases, true, nil
		}
	}
	return releases, false, nil
}

// isLastNeededReleasesPage reports whether no later page can hold a dexcli release newer than baselineVersion.
func isLastNeededReleasesPage(pageReleases []Release, baselineVersion string) bool {
	return len(pageReleases) < releasesPageSize || !isReleaseVersion(baselineVersion) || hasCLIReleaseAtOrBefore(pageReleases, baselineVersion)
}

func hasCLIReleaseAtOrBefore(releases []Release, baselineVersion string) bool {
	for _, release := range releases {
		if isCLIReleaseTag(release.TagName) && !isNewerVersion(release.TagName, baselineVersion) {
			return true
		}
	}
	return false
}

func (checker *releaseChecker) releasesPage(ctx context.Context, pageURL string) (_ []Release, returnErr error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
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
