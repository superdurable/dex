// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	tea "charm.land/bubbletea/v2"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// pendingRow is one row of the pending bar that Tab and Up/Down focus.
type pendingRow struct {
	target    pendingFocusTarget
	messageID string
}

// pendingRows lists the focusable rows top down: the waiting interrupts,
// then each queued message.
func (screen *ChatScreen) pendingRows() []pendingRow {
	var rows []pendingRow
	if screen.waitingInterruptCount() > 0 {
		rows = append(rows, pendingRow{target: pendingFocusInterrupts})
	}
	if screen.canActOnQueuedMessage() {
		for _, message := range screen.queuedMessages {
			rows = append(rows, pendingRow{target: pendingFocusQueuedMessage, messageID: message.MessageID})
		}
	}
	return rows
}

func (screen *ChatScreen) focusedPendingRowIndex(rows []pendingRow) int {
	selectedID := screen.selectedQueuedMessage().MessageID
	for index, row := range rows {
		if row.target == screen.pendingFocus && (row.target != pendingFocusQueuedMessage || row.messageID == selectedID) {
			return index
		}
	}
	return 0
}

func (screen *ChatScreen) focusPendingRow(row pendingRow) {
	screen.pendingFocus = row.target
	if row.target == pendingFocusQueuedMessage {
		screen.selectedQueuedMessageID = row.messageID
	}
}

// selectedQueuedMessage returns the queued message s and x act on: the
// selected one, else the last. The queue must not be empty.
func (screen *ChatScreen) selectedQueuedMessage() sessionfeed.QueuedMessage {
	for _, message := range screen.queuedMessages {
		if message.MessageID == screen.selectedQueuedMessageID {
			return message
		}
	}
	if len(screen.queuedMessages) == 0 {
		return sessionfeed.QueuedMessage{}
	}
	return screen.queuedMessages[len(screen.queuedMessages)-1]
}

// isPrintableKey reports a key that types text.
func isPrintableKey(key tea.KeyPressMsg) bool {
	return key.Text != "" && key.Mod&(tea.ModCtrl|tea.ModAlt) == 0
}

// scheduleDeferredPopup starts the wait for an interrupt deferred by typing
// once the input is empty; typing again cancels the wait.
func (screen *ChatScreen) scheduleDeferredPopup() tea.Cmd {
	if !screen.isPopupDeferredByTyping {
		return nil
	}
	if !screen.input.IsEmpty() {
		screen.deferredPopupGeneration++
		screen.isDeferredPopupScheduled = false
		return nil
	}
	if screen.isDeferredPopupScheduled {
		return nil
	}
	screen.isDeferredPopupScheduled = true
	return screen.delayedMessage(deferredPopupDelay, deferredPopupDueMsg{generation: screen.deferredPopupGeneration})
}

// openDeferredPopup opens the waiting interrupt when the input stayed empty
// and nothing else has focus.
func (screen *ChatScreen) openDeferredPopup(generation int) {
	if generation != screen.deferredPopupGeneration {
		return
	}
	screen.isDeferredPopupScheduled = false
	isFocusElsewhere := screen.isPopupOpen || screen.isOtherViewOpen() || screen.pendingFocus != pendingFocusNone || screen.pendingDelete != nil
	if !screen.isPopupDeferredByTyping || !screen.input.IsEmpty() || isFocusElsewhere || screen.isInputDisabled {
		return
	}
	screen.openPopup(screen.nextOpenableInterrupt(-1))
	screen.isPopupDeferredByTyping = false
}

// openPlanPager shows every plan step; the plan line shows only progress.
func (screen *ChatScreen) openPlanPager() {
	if len(screen.plan.Items) == 0 || screen.plan.Status == sessionfeed.PlanStatusCleared {
		screen.printNotice(sessionfeed.NoticeSeverityInfo, "no plan yet")
		return
	}
	screen.pager = newPlanPager(screen.plan)
}

// refreshPlanPager shows the newest plan in an open plan pager.
func (screen *ChatScreen) refreshPlanPager() {
	if screen.pager == nil || screen.pager.plan == nil {
		return
	}
	plan := screen.plan
	screen.pager.plan = &plan
}
