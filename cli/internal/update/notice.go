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
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	ansiReset       = "\033[0m"
	ansiWarning     = "\033[1;33m"
	ansiInformation = "\033[1;36m"
	ansiBreaking    = "\033[1;91m"
)

// PrintNotice writes available dexcli updates to output. Failures to contact GitHub are intentionally silent.
func PrintNotice(ctx context.Context, output io.Writer, currentVersion string) {
	printNotice(ctx, output, currentVersion, newReleaseChecker())
}

func printNotice(ctx context.Context, output io.Writer, currentVersion string, checker *releaseChecker) {
	if !isReleaseVersion(currentVersion) {
		return
	}
	releases, err := checker.Releases(ctx)
	if err != nil {
		return
	}
	newerReleases := releasesNewerThan(releases, currentVersion)
	if len(newerReleases) == 0 {
		return
	}
	latestRelease := newerReleases[len(newerReleases)-1]
	fmt.Fprintf(output, "\n%sA new dexcli version is available: %s (you have %s).%s\n", ansiWarning, latestRelease.TagName, currentVersion, ansiReset)
	fmt.Fprintf(output, "%sUpgrade with: brew update && brew upgrade dexcli%s\n", ansiInformation, ansiReset)
	for _, release := range newerReleases {
		fmt.Fprintf(output, "\n%sRelease notes for %s:%s\n", ansiInformation, release.TagName, ansiReset)
		writeReleaseNotes(output, release.Body)
	}
}

func releasesNewerThan(releases []Release, currentVersion string) []Release {
	newerReleases := make([]Release, 0, len(releases))
	for _, release := range releases {
		if release.IsDraft || release.IsPrerelease || !isCLIReleaseTag(release.TagName) || !isNewerVersion(release.TagName, currentVersion) {
			continue
		}
		newerReleases = append(newerReleases, release)
	}
	sort.Slice(newerReleases, func(leftIndex int, rightIndex int) bool {
		return isNewerVersion(newerReleases[rightIndex].TagName, newerReleases[leftIndex].TagName)
	})
	return newerReleases
}

func writeReleaseNotes(output io.Writer, releaseNotes string) {
	if strings.TrimSpace(releaseNotes) == "" {
		fmt.Fprintln(output, "No release notes were provided.")
		return
	}
	isBreakingChanges := false
	for _, line := range strings.Split(strings.ReplaceAll(releaseNotes, "\x1b", ""), "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "## Breaking Changes" {
			isBreakingChanges = true
		} else if strings.HasPrefix(trimmedLine, "## ") {
			isBreakingChanges = false
		}
		if isBreakingChanges {
			fmt.Fprintf(output, "%s%s%s\n", ansiBreaking, line, ansiReset)
			continue
		}
		fmt.Fprintln(output, line)
	}
}
