// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package remotesessionfeed

import (
	"testing"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

func approvalWithDisplay(display *dexagents.AgentInterruptDisplay) sessionfeed.ApprovalRequested {
	interrupt := dexagents.AgentInterrupt{
		InterruptID: "call-1", Kind: dexagents.InterruptKindToolApproval, ToolCallID: "call-1",
		ToolName: "write_file", Arguments: `{"path":"notes/a.txt","content":"new two"}`, Display: display,
	}
	return interruptUpdate(interrupt, nil).(sessionfeed.ApprovalRequested)
}

func approvalWithPreview(preview string) sessionfeed.ApprovalRequested {
	return approvalWithDisplay(&dexagents.AgentInterruptDisplay{Target: "notes/a.txt", Summary: "Writes notes/a.txt.", Preview: &preview})
}

const diffHeaders = "--- notes/a.txt (the file now)\n+++ notes/a.txt (after this write)\n"

// The tool source's display names the target, states the effect, and shows
// the diff of a file write.
func TestApprovalDisplayFillsThePopup(t *testing.T) {
	approval := approvalWithPreview(diffHeaders + "@@ -1,3 +1,3 @@ func main\n line one\n-old two\n+new two\n line three\n")
	require.Equal(t, "notes/a.txt", approval.ScopeLabel)
	require.Equal(t, "Writes notes/a.txt.", approval.EffectSummary)
	require.NotNil(t, approval.Preview)
	require.Equal(t, sessionfeed.FileChangePreview{
		Path: "notes/a.txt", AddedLineCount: 1, RemovedLineCount: 1,
		Lines: []sessionfeed.DiffLine{
			{Kind: sessionfeed.DiffLineContext, LineNumber: 1, Text: "line one"},
			{Kind: sessionfeed.DiffLineRemoved, LineNumber: 2, Text: "old two"},
			{Kind: sessionfeed.DiffLineAdded, LineNumber: 2, Text: "new two"},
			{Kind: sessionfeed.DiffLineContext, LineNumber: 3, Text: "line three"},
		},
	}, *approval.Preview)
	require.Len(t, approval.Arguments, 2, "the concrete arguments stay")
}

func TestApprovalDisplayOfNewFilesAndInsertions(t *testing.T) {
	created := approvalWithPreview("--- /dev/null (no file now)\n+++ notes/plain.txt (after this write)\n@@ -0,0 +1 @@\n+The token expired yesterday.\n")
	require.NotNil(t, created.Preview)
	require.Equal(t, "notes/plain.txt", created.Preview.Path)
	require.Equal(t, []sessionfeed.DiffLine{{Kind: sessionfeed.DiffLineAdded, LineNumber: 1, Text: "The token expired yesterday."}}, created.Preview.Lines)
	inserted := approvalWithPreview(diffHeaders + "@@ -5,0 +6,1 @@\n+inserted\n@@ -9,1 +10,1 @@\n-nine\n+ten\n")
	require.NotNil(t, inserted.Preview, "a hunk may start after a line and add only")
	require.Equal(t, 6, inserted.Preview.Lines[0].LineNumber)
	require.Equal(t, 9, inserted.Preview.Lines[1].LineNumber)
	require.Equal(t, 10, inserted.Preview.Lines[2].LineNumber)
}

// The marker says which side ends without a newline.
func TestApprovalDisplayKeepsTheNoNewlineMarker(t *testing.T) {
	marker := "\\ No newline at end of file\n"
	addsNewline := approvalWithPreview(diffHeaders + "@@ -1 +1 @@\n-last\n" + marker + "+last\n")
	require.NotNil(t, addsNewline.Preview)
	require.True(t, addsNewline.Preview.Lines[0].IsMissingFinalNewline, "the file now lacks it")
	require.False(t, addsNewline.Preview.Lines[1].IsMissingFinalNewline)
	dropsNewline := approvalWithPreview(diffHeaders + "@@ -1 +1 @@\n-last\n+last\n" + marker)
	require.NotNil(t, dropsNewline.Preview)
	require.False(t, dropsNewline.Preview.Lines[0].IsMissingFinalNewline)
	require.True(t, dropsNewline.Preview.Lines[1].IsMissingFinalNewline, "the write leaves it out")
	both := approvalWithPreview(diffHeaders + "@@ -1,2 +1,2 @@\n-one\n+uno\n last\n" + marker)
	require.NotNil(t, both.Preview)
	require.True(t, both.Preview.Lines[2].IsMissingFinalNewline)
}

// Untrusted preview text that is not a strict diff shows as it is.
func TestApprovalDisplayShowsMalformedDiffsAsText(t *testing.T) {
	for name, preview := range map[string]string{
		"no old header":         "+++ notes/a.txt\n@@ -1 +1 @@\n-a\n+b\n",
		"headers out of order":  "+++ notes/a.txt\n--- notes/a.txt\n@@ -1 +1 @@\n-a\n+b\n",
		"malformed hunk":        diffHeaders + "@@ malformed\n-a\n+b\n",
		"no hunk":               diffHeaders,
		"body before a hunk":    diffHeaders + "-a\n@@ -1 +1 @@\n-a\n+b\n",
		"too few lines":         diffHeaders + "@@ -1,2 +1,2 @@\n-a\n+b\n",
		"too many lines":        diffHeaders + "@@ -1 +1 @@\n-a\n+b\n+c\n",
		"huge line number":      diffHeaders + "@@ -99999999 +99999999 @@\n-a\n+b\n",
		"line zero with lines":  diffHeaders + "@@ -0,1 +0,1 @@\n-a\n+b\n",
		"overlapping hunks":     diffHeaders + "@@ -5,2 +5,2 @@\n a\n b\n@@ -4 +4 @@\n c\n",
		"unknown line":          diffHeaders + "@@ -1 +1 @@\n*a\n",
		"empty body line":       diffHeaders + "@@ -1,2 +1,2 @@\n a\n\n",
		"other backslash line":  diffHeaders + "@@ -1 +1 @@\n-a\n\\ something else\n+b\n",
		"marker first":          diffHeaders + "@@ -1 +1 @@\n\\ No newline at end of file\n-a\n+b\n",
		"two markers":           diffHeaders + "@@ -1 +1 @@\n-a\n\\ No newline at end of file\n\\ No newline at end of file\n+b\n",
		"line after the marker": diffHeaders + "@@ -1,2 +1,2 @@\n-a\n\\ No newline at end of file\n-b\n+c\n+d\n",
		"both sides /dev/null":  "--- /dev/null\n+++ /dev/null\n@@ -0,0 +0,0 @@\n",
		"plain text":            "cd ~/workspace\ngo test ./...",
	} {
		approval := approvalWithPreview(preview)
		require.Nil(t, approval.Preview, name)
		require.Equal(t, sessionfeed.ApprovalArgument{Name: "preview", Value: preview}, approval.Arguments[len(approval.Arguments)-1], name)
	}
}

func TestApprovalWithoutDisplayShowsTheArguments(t *testing.T) {
	bare := approvalWithDisplay(nil)
	require.Empty(t, bare.ScopeLabel)
	require.Empty(t, bare.EffectSummary)
	require.Nil(t, bare.Preview)
	require.Len(t, bare.Arguments, 2)
}
