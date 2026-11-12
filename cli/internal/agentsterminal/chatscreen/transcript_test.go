// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// fillTranscript adds count reply blocks whose every word carries the block's
// tag.
func fillTranscript(harness *screenHarness, count int) {
	for block := range count {
		words := make([]string, 0, 30)
		for word := range 30 {
			words = append(words, fmt.Sprintf("b%04dw%02d", block, word))
		}
		harness.screen.printReplyOutputs([]replyOutput{{kind: replyOutputMarkdown, text: strings.Join(words, " ")}})
	}
}

var blockTagPattern = regexp.MustCompile(`b\d{4}`)

// topBlockTag returns the block tag of the first message row that has one.
func topBlockTag(t *testing.T, content string) string {
	t.Helper()
	for _, line := range splitLines(stripForTest(content)) {
		if tag := blockTagPattern.FindString(line); tag != "" {
			return tag
		}
	}
	t.Fatalf("no block tag on the screen:\n%s", stripForTest(content))
	return ""
}

func TestScrolledViewKeepsItsPlaceAcrossResizes(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 140, height: 30})
	fillTranscript(harness, 60)
	harness.press(key(tea.KeyPgUp), key(tea.KeyPgUp), key(tea.KeyPgUp))
	require.False(t, harness.screen.scroll.isFollowing)
	before := harness.screen.View().Content
	tag := topBlockTag(t, before)

	harness.screen.Update(tea.WindowSizeMsg{Width: 40, Height: 30})
	narrow := harness.screen.View().Content
	requireWellFormedScreen(t, narrow, 40, 30)
	require.Equal(t, tag, topBlockTag(t, narrow), "the narrow view starts in the block the reader was in")

	harness.screen.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	require.Equal(t, before, harness.screen.View().Content, "returning to 140 columns restores the same view")
	require.False(t, harness.screen.scroll.isFollowing)
}

// countingBlock counts how often it is laid out.
type countingBlock struct {
	renderCount *int
}

func (block countingBlock) Render(int, terminaltheme.Theme) []string {
	*block.renderCount++
	return []string{"counted"}
}

func TestTranscriptLaysOutAppendsIncrementally(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	renderCount := 0
	harness.screen.appendBlock(transcriptBlockReply, countingBlock{renderCount: &renderCount})
	first := append([]string{}, harness.screen.history.Lines(100)...)
	for index := range 5 {
		harness.screen.printNotice(sessionfeed.NoticeSeverityInfo, fmt.Sprintf("note %d", index))
		harness.screen.View()
	}
	second := harness.screen.history.Lines(100)
	require.Equal(t, first, second[:len(first)], "earlier lines stay as they were")
	require.Equal(t, 1, renderCount, "appends and redraws do not lay out earlier blocks again")
	harness.screen.Update(tea.WindowSizeMsg{Width: 60, Height: 40})
	harness.screen.View()
	require.Equal(t, 2, renderCount, "a width change lays everything out once")
}

// frameAllocations measures one redraw of a screen with count blocks.
func frameAllocations(t *testing.T, count int) float64 {
	harness := newScreenHarness(t, harnessOptions{width: 100, height: 40})
	fillTranscript(harness, count)
	startTurn(harness, "Go.")
	harness.screen.View()
	return testing.AllocsPerRun(20, func() {
		harness.screen.View()
	})
}

func TestRedrawCostDoesNotGrowWithTheTranscript(t *testing.T) {
	short := frameAllocations(t, 200)
	long := frameAllocations(t, 2000)
	require.LessOrEqual(t, long, short*1.1+10, "a redraw allocates %.0f times with 2,000 blocks and %.0f with 200", long, short)
}

func BenchmarkRedrawWithLongTranscript(b *testing.B) {
	harness := newScreenHarness(b, harnessOptions{width: 100, height: 40})
	fillTranscript(harness, 2000)
	harness.screen.View()
	b.ReportAllocs()
	for b.Loop() {
		harness.screen.View()
	}
}
