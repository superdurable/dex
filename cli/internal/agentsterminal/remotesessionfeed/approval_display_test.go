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

// The tool source's display names the target, states the effect, and shows
// the diff of a file write.
func TestApprovalDisplayFillsThePopup(t *testing.T) {
	preview := "--- notes/a.txt (the file now)\n+++ notes/a.txt (after this write)\n@@ -1,3 +1,3 @@\n line one\n-old two\n+new two\n line three\n\\ No newline at end of file\n"
	approval := approvalWithDisplay(&dexagents.AgentInterruptDisplay{Target: "notes/a.txt", Summary: "Writes notes/a.txt in the workspace.", Preview: &preview})
	require.Equal(t, "notes/a.txt", approval.ScopeLabel)
	require.Equal(t, "Writes notes/a.txt in the workspace.", approval.EffectSummary)
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

func TestApprovalDisplayOfANewFile(t *testing.T) {
	preview := "--- /dev/null (no file now)\n+++ notes/plain.txt (after this write)\n@@ -0,0 +1 @@\n+The token expired yesterday.\n"
	approval := approvalWithDisplay(&dexagents.AgentInterruptDisplay{Target: "notes/plain.txt", Summary: "Creates notes/plain.txt.", Preview: &preview})
	require.NotNil(t, approval.Preview)
	require.Equal(t, "notes/plain.txt", approval.Preview.Path)
	require.Equal(t, []sessionfeed.DiffLine{{Kind: sessionfeed.DiffLineAdded, LineNumber: 1, Text: "The token expired yesterday."}}, approval.Preview.Lines)
}

// A preview that is not a diff stays visible as text; no display leaves the
// concrete arguments alone.
func TestApprovalDisplayWithoutADiff(t *testing.T) {
	preview := "cd ~/workspace\ngo test ./..."
	approval := approvalWithDisplay(&dexagents.AgentInterruptDisplay{Target: "~/workspace", Summary: "Runs go test ./... in ~/workspace.", Preview: &preview})
	require.Nil(t, approval.Preview)
	require.Equal(t, sessionfeed.ApprovalArgument{Name: "preview", Value: preview}, approval.Arguments[len(approval.Arguments)-1])
	bare := approvalWithDisplay(nil)
	require.Empty(t, bare.ScopeLabel)
	require.Empty(t, bare.EffectSummary)
	require.Nil(t, bare.Preview)
	require.Len(t, bare.Arguments, 2)
}
