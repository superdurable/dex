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
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	releaseNotesWidth       = 100
	releaseNotesHTTPTimeout = 10 * time.Second
	// releaseNotesMaxPages bounds paging through the releases of every Dex component.
	releaseNotesMaxPages = 5
	ansiBold             = "\033[1m"
	breakingSectionTitle = "Breaking Changes"
	fullChangeLogPrefix  = "Full change log:"
)

var markdownLinkPattern = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)

// releaseNotesSection is one "## Title" part of a release body.
type releaseNotesSection struct {
	title string
	lines []string
}

// parsedReleaseNotes is a release body split into sections, with its change-log links removed.
type parsedReleaseNotes struct {
	sections       []releaseNotesSection
	changeLogLinks []string
}

// ExecuteReleaseNotes runs dexcli release-notes. It prints the dexcli releases after a version, newest first.
func ExecuteReleaseNotes(ctx context.Context, args []string, stdout io.Writer, currentVersion string, isStdoutTerminal bool) error {
	checker := newReleaseChecker()
	checker.httpClient.Timeout = releaseNotesHTTPTimeout
	return executeReleaseNotes(ctx, args, stdout, currentVersion, isStdoutTerminal, checker)
}

func executeReleaseNotes(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	currentVersion string,
	isStdoutTerminal bool,
	checker *releaseChecker,
) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		printReleaseNotesUsage(stdout)
		return nil
	}
	if len(args) > 1 {
		return fmt.Errorf("dexcli release-notes takes at most one version, got %d arguments", len(args))
	}
	baselineVersion := currentVersion
	if len(args) == 1 {
		if !isReleaseVersion(args[0]) {
			return fmt.Errorf("dexcli release-notes: %q is not a version such as v1.47.9", args[0])
		}
		baselineVersion = args[0]
	}
	releases, err := checker.ReleasesAfter(ctx, baselineVersion, releaseNotesMaxPages)
	if err != nil {
		return err
	}
	var selectedReleases []Release
	if isReleaseVersion(baselineVersion) {
		selectedReleases = releasesNewerThan(releases, baselineVersion)
	} else if newestReleases := releasesNewerThan(releases, "v0.0.0"); len(newestReleases) > 0 {
		// A development build has no baseline, so it shows the newest release only.
		selectedReleases = newestReleases[len(newestReleases)-1:]
	}
	if len(selectedReleases) == 0 {
		fmt.Fprintf(stdout, "No dexcli release is newer than %s.\n", displayVersion(baselineVersion))
		fmt.Fprintln(stdout, "Pass an older version to see the releases after it, such as: dexcli release-notes v1.47.0")
		return nil
	}
	writeReleaseNotesReport(stdout, selectedReleases, baselineVersion, isStdoutTerminal)
	return nil
}

func printReleaseNotesUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: dexcli release-notes [VERSION]")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Print the notes of every dexcli release after VERSION, newest first.")
	fmt.Fprintln(output, "VERSION defaults to the running dexcli version. A development build shows the newest release.")
}

// writeReleaseNotesReport writes releases, given oldest first, as one report with the newest release first.
func writeReleaseNotesReport(output io.Writer, releases []Release, baselineVersion string, useColor bool) {
	style := releaseNotesStyle{useColor: useColor}
	latestVersion := displayVersion(releases[len(releases)-1].TagName)
	if isReleaseVersion(baselineVersion) {
		fmt.Fprintf(output, "%s\n", style.wrap(ansiWarning, fmt.Sprintf("dexcli release notes since %s, through %s (%s)",
			displayVersion(baselineVersion), latestVersion, countReleases(len(releases)))))
	} else {
		fmt.Fprintf(output, "%s\n", style.wrap(ansiWarning, "dexcli release notes for "+latestVersion))
	}
	var changeLogLinks []string
	for index := len(releases) - 1; index >= 0; index-- {
		release := releases[index]
		notes := parseReleaseNotes(release.Body)
		for _, link := range notes.changeLogLinks {
			if !slices.Contains(changeLogLinks, link) {
				changeLogLinks = append(changeLogLinks, link)
			}
		}
		fmt.Fprintln(output)
		heading := displayVersion(release.TagName)
		if date := releaseDate(release.PublishedAt); date != "" {
			heading += "  (" + date + ")"
		}
		fmt.Fprintln(output, style.wrap(ansiInformation, heading))
		writeReleaseSections(output, notes.sections, style)
	}
	for _, link := range changeLogLinks {
		fmt.Fprintln(output)
		fmt.Fprintf(output, "%s %s\n", fullChangeLogPrefix, link)
	}
}

// writeReleaseSections writes the Breaking Changes section first and skips sections that say only "None.".
func writeReleaseSections(output io.Writer, sections []releaseNotesSection, style releaseNotesStyle) {
	var orderedSections []releaseNotesSection
	for _, section := range sections {
		if section.title == breakingSectionTitle && !isEmptySection(section) {
			orderedSections = append(orderedSections, section)
		}
	}
	for _, section := range sections {
		if section.title != breakingSectionTitle && !isEmptySection(section) {
			orderedSections = append(orderedSections, section)
		}
	}
	if len(orderedSections) == 0 {
		fmt.Fprintln(output, "  No release notes were provided.")
		return
	}
	for _, section := range orderedSections {
		isBreaking := section.title == breakingSectionTitle
		if section.title != "" {
			titleColor := ansiBold
			if isBreaking {
				titleColor = ansiBreaking
			}
			fmt.Fprintf(output, "  %s\n", style.wrap(titleColor, section.title))
		}
		for _, item := range sectionItems(section.lines) {
			firstIndent, nextIndent := "  ", "  "
			if strings.HasPrefix(item, "- ") {
				firstIndent, nextIndent, item = "  - ", "    ", strings.TrimPrefix(item, "- ")
			}
			for _, line := range wrapMarkdownText(item, releaseNotesWidth-len(firstIndent)) {
				rendered := style.renderInline(line)
				if isBreaking {
					rendered = style.wrap(ansiBreaking, strings.ReplaceAll(line, "**", ""))
				}
				fmt.Fprintf(output, "%s%s\n", firstIndent, rendered)
				firstIndent = nextIndent
			}
		}
	}
}

// hasBreakingChanges reports whether a release body lists a breaking change other than "None.".
func hasBreakingChanges(releaseBody string) bool {
	for _, section := range parseReleaseNotes(releaseBody).sections {
		if section.title == breakingSectionTitle && !isEmptySection(section) {
			return true
		}
	}
	return false
}

func parseReleaseNotes(releaseBody string) parsedReleaseNotes {
	var notes parsedReleaseNotes
	current := releaseNotesSection{}
	for _, line := range strings.Split(strings.ReplaceAll(releaseBody, "\x1b", ""), "\n") {
		trimmedLine := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmedLine, "#"):
			notes.sections = append(notes.sections, current)
			current = releaseNotesSection{title: strings.TrimSpace(strings.TrimLeft(trimmedLine, "#"))}
		case strings.HasPrefix(trimmedLine, fullChangeLogPrefix):
			notes.changeLogLinks = append(notes.changeLogLinks, strings.TrimSpace(strings.TrimPrefix(trimmedLine, fullChangeLogPrefix)))
		default:
			current.lines = append(current.lines, strings.TrimRight(line, " \t\r"))
		}
	}
	notes.sections = append(notes.sections, current)
	return notes
}

func isEmptySection(section releaseNotesSection) bool {
	text := strings.TrimSpace(strings.Join(section.lines, " "))
	return text == "" || text == "None."
}

// sectionItems joins wrapped source lines into bullets (prefixed "- ") and paragraphs.
func sectionItems(lines []string) []string {
	var items []string
	var current []string
	flush := func() {
		if len(current) > 0 {
			items = append(items, strings.Join(current, " "))
			current = nil
		}
	}
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		switch {
		case trimmedLine == "":
			flush()
		case strings.HasPrefix(trimmedLine, "- ") || strings.HasPrefix(trimmedLine, "* "):
			flush()
			current = append(current, "- "+strings.TrimSpace(trimmedLine[2:]))
		default:
			current = append(current, trimmedLine)
		}
	}
	flush()
	return items
}

// wrapMarkdownText wraps text at width visible characters; "**" markers and link targets count as written.
func wrapMarkdownText(text string, width int) []string {
	text = markdownLinkPattern.ReplaceAllString(text, "$1 ($2)")
	var lines []string
	var line strings.Builder
	lineWidth := 0
	for _, word := range strings.Fields(text) {
		wordWidth := utf8.RuneCountInString(strings.ReplaceAll(word, "**", ""))
		if lineWidth > 0 && lineWidth+1+wordWidth > width {
			lines = append(lines, line.String())
			line.Reset()
			lineWidth = 0
		}
		if lineWidth > 0 {
			line.WriteByte(' ')
			lineWidth++
		}
		line.WriteString(word)
		lineWidth += wordWidth
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return lines
}

// releaseNotesStyle applies ANSI styles only when stdout is a terminal.
type releaseNotesStyle struct {
	useColor bool
}

func (style releaseNotesStyle) wrap(ansiCode string, text string) string {
	if !style.useColor {
		return text
	}
	return ansiCode + text + ansiReset
}

// renderInline turns "**bold**" markers into bold text, or drops them without color.
func (style releaseNotesStyle) renderInline(line string) string {
	if !strings.Contains(line, "**") {
		return line
	}
	if !style.useColor {
		return strings.ReplaceAll(line, "**", "")
	}
	parts := strings.Split(line, "**")
	var rendered strings.Builder
	for index, part := range parts {
		if index%2 == 1 && index < len(parts)-1 {
			rendered.WriteString(ansiBold + part + ansiReset)
			continue
		}
		rendered.WriteString(part)
	}
	return rendered.String()
}

func displayVersion(version string) string {
	return strings.TrimPrefix(version, "cli-")
}

func releaseDate(publishedAt string) string {
	published, err := time.Parse(time.RFC3339, publishedAt)
	if err != nil {
		return ""
	}
	return published.Format("2006-01-02")
}

func countReleases(count int) string {
	if count == 1 {
		return "1 release"
	}
	return fmt.Sprintf("%d releases", count)
}
