// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// pendingInterrupt is a waiting approval or question; exactly one field is
// set.
type pendingInterrupt struct {
	approval     *sessionfeed.ApprovalRequested
	question     *sessionfeed.QuestionAsked
	isSubmitting bool
}

func (interrupt pendingInterrupt) InterruptID() string {
	if interrupt.approval != nil {
		return interrupt.approval.InterruptID
	}
	return interrupt.question.InterruptID
}

// interruptPopup is the open approval or question popup.
type interruptPopup struct {
	selectedOption        int
	isEnteringText        bool
	textInput             inputEditor
	isShowingAllArguments bool
}

// popupKeyResult is what a key did in the popup.
type popupKeyResult struct {
	action   sessionfeed.UserAction
	feedback string
	// outcomeNote is printed once the session confirms the action; empty
	// when the tool row already shows the outcome.
	outcomeNote     string
	isClosed        bool
	isNextRequested bool
}

const (
	approvalOptionApprove = iota
	approvalOptionReject
	approvalOptionRejectWithReason
	approvalOptionCount
)

// questionOption is one row of the question popup.
type questionOption struct {
	label       string
	choice      string
	isFreeText  bool
	isDeclining bool
}

func newInterruptPopup(interrupt pendingInterrupt) interruptPopup {
	popup := interruptPopup{}
	if question := interrupt.question; question != nil && len(question.Choices) == 0 && question.AllowsFreeText {
		popup.isEnteringText = true
	}
	return popup
}

// HandleKey applies one key to the popup of interrupt.
func (popup *interruptPopup) HandleKey(interrupt pendingInterrupt, key tea.KeyPressMsg) popupKeyResult {
	if popup.isEnteringText {
		return popup.handleTextKey(interrupt, key)
	}
	switch key.String() {
	case "esc":
		return popupKeyResult{isClosed: true}
	case "tab":
		return popupKeyResult{isNextRequested: true}
	case "up", "k":
		popup.selectedOption = max(popup.selectedOption-1, 0)
		return popupKeyResult{}
	case "down", "j":
		popup.selectedOption = min(popup.selectedOption+1, popup.optionCount(interrupt)-1)
		return popupKeyResult{}
	}
	if interrupt.approval != nil {
		return popup.handleApprovalKey(*interrupt.approval, key)
	}
	return popup.handleQuestionKey(*interrupt.question, key)
}

func (popup *interruptPopup) handleApprovalKey(approval sessionfeed.ApprovalRequested, key tea.KeyPressMsg) popupKeyResult {
	switch key.String() {
	case "y":
		return approveResult(approval)
	case "n":
		return rejectResult(approval, "")
	case "r":
		popup.selectedOption = approvalOptionRejectWithReason
		popup.isEnteringText = true
		return popupKeyResult{}
	case "e":
		popup.isShowingAllArguments = !popup.isShowingAllArguments
		return popupKeyResult{}
	case "enter":
		switch popup.selectedOption {
		case approvalOptionApprove:
			return approveResult(approval)
		case approvalOptionReject:
			return rejectResult(approval, "")
		default:
			popup.isEnteringText = true
			return popupKeyResult{}
		}
	}
	return popupKeyResult{}
}

func (popup *interruptPopup) handleQuestionKey(question sessionfeed.QuestionAsked, key tea.KeyPressMsg) popupKeyResult {
	if key.String() != "enter" {
		return popupKeyResult{}
	}
	options := questionOptions(question)
	option := options[min(popup.selectedOption, len(options)-1)]
	switch {
	case option.isDeclining:
		return popupKeyResult{
			action:      sessionfeed.DeclineQuestion{InterruptID: question.InterruptID},
			feedback:    "declining…",
			outcomeNote: "declined the question",
		}
	case option.isFreeText:
		popup.isEnteringText = true
		return popupKeyResult{}
	default:
		return answerResult(question, option.choice)
	}
}

func (popup *interruptPopup) handleTextKey(interrupt pendingInterrupt, key tea.KeyPressMsg) popupKeyResult {
	switch key.String() {
	case "esc":
		if interrupt.question != nil && len(interrupt.question.Choices) == 0 {
			return popupKeyResult{isClosed: true}
		}
		popup.isEnteringText = false
		popup.textInput.Clear()
		return popupKeyResult{}
	case "tab":
		return popupKeyResult{isNextRequested: true}
	case "down":
		if interrupt.question != nil && len(interrupt.question.Choices) == 0 {
			popup.isEnteringText = false
			popup.selectedOption = popup.optionCount(interrupt) - 1
		}
		return popupKeyResult{}
	case "enter":
		text := strings.TrimSpace(popup.textInput.Value())
		if interrupt.approval != nil {
			return rejectResult(*interrupt.approval, text)
		}
		if text == "" {
			return popupKeyResult{}
		}
		return answerResult(*interrupt.question, text)
	}
	popup.textInput.HandleEditingKey(key)
	return popupKeyResult{}
}

func (popup *interruptPopup) optionCount(interrupt pendingInterrupt) int {
	if interrupt.approval != nil {
		return approvalOptionCount
	}
	return len(questionOptions(*interrupt.question))
}

func questionOptions(question sessionfeed.QuestionAsked) []questionOption {
	options := make([]questionOption, 0, len(question.Choices)+2)
	for _, choice := range question.Choices {
		options = append(options, questionOption{label: choice, choice: choice})
	}
	if question.AllowsFreeText {
		options = append(options, questionOption{label: "Type an answer…", isFreeText: true})
	}
	return append(options, questionOption{label: "Decline", isDeclining: true})
}

func approveResult(approval sessionfeed.ApprovalRequested) popupKeyResult {
	return popupKeyResult{
		action:   sessionfeed.ResolveApproval{InterruptID: approval.InterruptID, IsApproved: true},
		feedback: "approving " + approval.ToolName + "…",
	}
}

func rejectResult(approval sessionfeed.ApprovalRequested, reason string) popupKeyResult {
	return popupKeyResult{
		action:   sessionfeed.ResolveApproval{InterruptID: approval.InterruptID, Reason: reason},
		feedback: "rejecting " + approval.ToolName + "…",
	}
}

func answerResult(question sessionfeed.QuestionAsked, answer string) popupKeyResult {
	return popupKeyResult{
		action:      sessionfeed.AnswerQuestion{InterruptID: question.InterruptID, Answer: answer},
		feedback:    "answering…",
		outcomeNote: "answered: " + answer,
	}
}
