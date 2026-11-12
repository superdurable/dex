// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package sessionfeed

import "time"

// ToolCallStatus is the display state of one tool call.
type ToolCallStatus int

const (
	// ToolCallQueued waits for a timer, a task, or a write slot.
	ToolCallQueued ToolCallStatus = iota
	// ToolCallRunning is running.
	ToolCallRunning
	// ToolCallAwaitingApproval waits for the user's approval.
	ToolCallAwaitingApproval
	// ToolCallAwaitingAnswer is an ask_user call that waits for the user's
	// answer.
	ToolCallAwaitingAnswer
	// ToolCallSucceeded finished successfully.
	ToolCallSucceeded
	// ToolCallFailed finished with an error.
	ToolCallFailed
	// ToolCallOutcomeUnknown ended without a known result.
	ToolCallOutcomeUnknown
	// ToolCallRejected was rejected by the user.
	ToolCallRejected
	// ToolCallCancelled was cancelled with its turn.
	ToolCallCancelled
	// ToolCallSkipped did not run.
	ToolCallSkipped
	// ToolCallFromHistory comes from stored messages, which keep no
	// reliable status; Summary holds the output's first line.
	ToolCallFromHistory
)

// IsFinished reports a final state. Finished rows move into the scrollback.
func (status ToolCallStatus) IsFinished() bool {
	return status >= ToolCallSucceeded
}

// ToolCallChanged is the current state of one tool call. It replaces earlier
// updates with its ToolCallID.
type ToolCallChanged struct {
	// ToolCallID identifies the call.
	ToolCallID string
	// TurnID identifies the turn that made the call; empty when unknown.
	TurnID string
	// ToolName is the tool name, such as "read_file".
	ToolName string
	// KeyArgument identifies the call: a workspace path, a shell command, or a
	// connector's first argument.
	KeyArgument string
	// Status is the display state.
	Status ToolCallStatus
	// StartedAt is when the call started; running rows show the elapsed
	// time from it.
	StartedAt time.Time
	// Duration is how long a finished call took.
	Duration time.Duration
	// Summary is the short result, such as "12 matches", "exit 0", or
	// "posted". It is shown on finished rows.
	Summary string
	// Reason explains a rejected, cancelled, or skipped call.
	Reason string
	// OutputTail holds the last lines of running output, or the first error
	// lines of a failure.
	OutputTail []string
	// HiddenOutputLineCount is how many output lines OutputTail leaves out.
	HiddenOutputLineCount int
	// IsOutputTruncated reports that the session stored only part of the
	// output.
	IsOutputTruncated bool
	// Arguments are the call's arguments in written order, for the pager.
	Arguments []ApprovalArgument
	// OutputText is the stored output, or the error of a failed call; the
	// pager shows it in full.
	OutputText string
	// ImageCount is how many images the tool returned with its output.
	ImageCount int
	// PlanItems are the steps a write_todos call wrote; nil for other
	// tools.
	PlanItems []PlanItem
}

// ApprovalArgument is one tool argument shown in an approval popup or the
// pager.
type ApprovalArgument struct {
	// Name is the argument name.
	Name string
	// Value is the argument value as text.
	Value string
}

// DiffLineKind is the kind of one diff preview line.
type DiffLineKind int

const (
	// DiffLineContext is an unchanged line.
	DiffLineContext DiffLineKind = iota
	// DiffLineAdded is an added line.
	DiffLineAdded
	// DiffLineRemoved is a removed line.
	DiffLineRemoved
)

// DiffLine is one line of a file change preview.
type DiffLine struct {
	// Kind is the line kind.
	Kind DiffLineKind
	// LineNumber is the line number in the file, or 0 when unknown.
	LineNumber int
	// Text is the line text without the diff sign.
	Text string
	// IsMissingFinalNewline reports that this line ends its side of the file
	// without a newline: the file now for a removed line, after the write
	// for an added line, and both for a context line.
	IsMissingFinalNewline bool
}

// FileChangePreview is a tool's preview of a file write, diffed against the
// file as it is now.
type FileChangePreview struct {
	// Path is the file path relative to the workspace.
	Path string
	// AddedLineCount is the number of added lines.
	AddedLineCount int
	// RemovedLineCount is the number of removed lines.
	RemovedLineCount int
	// Lines are the diff lines. Credential files have none.
	Lines []DiffLine
	// IsCredentialFile hides the content and shows only the counts.
	IsCredentialFile bool
}

// ApprovalRequested reports a tool call that waits for the user's approval.
type ApprovalRequested struct {
	// InterruptID identifies the approval for ResolveApproval.
	InterruptID string
	// ToolCallID identifies the tool call.
	ToolCallID string
	// ToolName is the tool name.
	ToolName string
	// SourceLabel names where the tool comes from, such as "connector" or
	// "local file".
	SourceLabel string
	// ScopeLabel names the connection or workspace, such as
	// "slack/support-slack".
	ScopeLabel string
	// EffectLabel is the tool effect, such as "write".
	EffectLabel string
	// Arguments are the concrete arguments in display order.
	Arguments []ApprovalArgument
	// Preview is the file change preview, or nil.
	Preview *FileChangePreview
	// EffectSummary states the call's concrete effect. Empty means the tool does
	// not describe it.
	EffectSummary string
	// Deadline is when the approval expires, or the zero time.
	Deadline time.Time
}

// QuestionAsked reports a question from the agent.
type QuestionAsked struct {
	// InterruptID identifies the question for AnswerQuestion and
	// DeclineQuestion.
	InterruptID string
	// Question is the question text.
	Question string
	// Choices are the offered answers; may be empty.
	Choices []string
	// AllowsFreeText reports whether the user may type an answer.
	AllowsFreeText bool
}

// InterruptResolution is how an approval or question was resolved.
type InterruptResolution int

const (
	// InterruptApproved means the approval was granted.
	InterruptApproved InterruptResolution = iota
	// InterruptRejected means the approval was rejected.
	InterruptRejected
	// InterruptAnswered means the question was answered.
	InterruptAnswered
	// InterruptDeclined means the question was declined.
	InterruptDeclined
	// InterruptWithdrawn means the interrupt ended without an answer, for
	// example because its turn was cancelled.
	InterruptWithdrawn
)

// InterruptResolved reports that an approval or question is no longer
// pending.
type InterruptResolved struct {
	// InterruptID identifies the approval or question.
	InterruptID string
	// Resolution is how it was resolved.
	Resolution InterruptResolution
	// ResolvedBy names another client that resolved it, such as "Dex Web";
	// empty when this screen resolved it.
	ResolvedBy string
}

func (ToolCallChanged) isSessionUpdate()   {}
func (ApprovalRequested) isSessionUpdate() {}
func (QuestionAsked) isSessionUpdate()     {}
func (InterruptResolved) isSessionUpdate() {}
