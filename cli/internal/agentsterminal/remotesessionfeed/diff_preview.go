// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package remotesessionfeed

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// hunkHeaderPattern matches "@@ -a,b +c,d @@" with optional counts and an
// optional section heading.
var hunkHeaderPattern = regexp.MustCompile(`^@@ -(\d{1,9})(?:,(\d{1,9}))? \+(\d{1,9})(?:,(\d{1,9}))? @@(?: .*)?$`)

const (
	// noNewlineMarker follows the last line of a side without a final newline.
	noNewlineMarker = `\ No newline at end of file`
	// maximumDiffLineNumber bounds hunk starts and counts as sane.
	maximumDiffLineNumber = 10_000_000
	// devNull names the missing side of a created or deleted file.
	devNull = "/dev/null"
)

// diffSide tracks one side of a hunk: the next line number, the lines the
// header still promises, and whether a no-newline marker ended it.
type diffSide struct {
	nextLine  int
	remaining int
	isEnded   bool
	// previousEnd is the line after the last hunk, so hunks never overlap.
	previousEnd int
}

// fileChangePreview reads text as a strict unified diff of one file and
// reports whether it was one. Anything malformed, incomplete, or with
// counts that disagree with the body returns false, so the caller shows the
// original text instead.
func fileChangePreview(text string) (sessionfeed.FileChangePreview, bool) {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[0], "--- ") || !strings.HasPrefix(lines[1], "+++ ") {
		return sessionfeed.FileChangePreview{}, false
	}
	path := withoutDiffLabel(strings.TrimPrefix(lines[1], "+++ "))
	if path == devNull {
		path = withoutDiffLabel(strings.TrimPrefix(lines[0], "--- "))
	}
	if path == "" || path == devNull {
		return sessionfeed.FileChangePreview{}, false
	}
	preview := sessionfeed.FileChangePreview{Path: path}
	var oldSide, newSide diffSide
	isInHunk := false
	for _, line := range lines[2:] {
		if match := hunkHeaderPattern.FindStringSubmatch(line); match != nil {
			if isInHunk && (oldSide.remaining != 0 || newSide.remaining != 0) {
				return sessionfeed.FileChangePreview{}, false
			}
			if !oldSide.startHunk(match[1], match[2]) || !newSide.startHunk(match[3], match[4]) {
				return sessionfeed.FileChangePreview{}, false
			}
			isInHunk = true
			continue
		}
		if !isInHunk || !appendDiffBodyLine(&preview, line, &oldSide, &newSide) {
			return sessionfeed.FileChangePreview{}, false
		}
	}
	isComplete := isInHunk && oldSide.remaining == 0 && newSide.remaining == 0
	return preview, isComplete
}

// startHunk applies one side's "start,count" of a hunk header and reports
// whether the numbers are sane and follow the previous hunk.
func (side *diffSide) startHunk(startText string, countText string) bool {
	start, err := strconv.Atoi(startText)
	if err != nil {
		return false
	}
	count := 1
	if countText != "" {
		if count, err = strconv.Atoi(countText); err != nil {
			return false
		}
	}
	// Only an empty side starts at line 0; "-5,0" inserts after line 5.
	isSane := start <= maximumDiffLineNumber && count <= maximumDiffLineNumber && (start != 0 || count == 0)
	if !isSane || side.isEnded || start < side.previousEnd {
		return false
	}
	side.nextLine, side.remaining = start, count
	side.previousEnd = start + count
	return true
}

// appendDiffBodyLine adds one hunk body line, or marks the previous line
// with the no-newline marker, and reports whether line was valid there.
func appendDiffBodyLine(preview *sessionfeed.FileChangePreview, line string, oldSide *diffSide, newSide *diffSide) bool {
	if line == noNewlineMarker {
		if len(preview.Lines) == 0 {
			return false
		}
		last := &preview.Lines[len(preview.Lines)-1]
		if last.IsMissingFinalNewline {
			return false
		}
		last.IsMissingFinalNewline = true
		if last.Kind != sessionfeed.DiffLineAdded {
			oldSide.isEnded = true
		}
		if last.Kind != sessionfeed.DiffLineRemoved {
			newSide.isEnded = true
		}
		return true
	}
	if line == "" {
		return false
	}
	diffLine := sessionfeed.DiffLine{Text: line[1:]}
	var sides []*diffSide
	switch line[0] {
	case ' ':
		diffLine.Kind, sides = sessionfeed.DiffLineContext, []*diffSide{oldSide, newSide}
		diffLine.LineNumber = newSide.nextLine
	case '-':
		diffLine.Kind, sides = sessionfeed.DiffLineRemoved, []*diffSide{oldSide}
		diffLine.LineNumber = oldSide.nextLine
	case '+':
		diffLine.Kind, sides = sessionfeed.DiffLineAdded, []*diffSide{newSide}
		diffLine.LineNumber = newSide.nextLine
	default:
		return false
	}
	for _, side := range sides {
		if side.remaining == 0 || side.isEnded {
			return false
		}
		side.remaining--
		side.nextLine++
	}
	switch diffLine.Kind {
	case sessionfeed.DiffLineAdded:
		preview.AddedLineCount++
	case sessionfeed.DiffLineRemoved:
		preview.RemovedLineCount++
	}
	preview.Lines = append(preview.Lines, diffLine)
	return true
}

// withoutDiffLabel removes a header's trailing label, such as
// " (after this write)".
func withoutDiffLabel(header string) string {
	if strings.HasSuffix(header, ")") {
		if index := strings.LastIndex(header, " ("); index > 0 {
			return header[:index]
		}
	}
	return header
}
