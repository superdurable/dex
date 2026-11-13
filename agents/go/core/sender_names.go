// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexagents

import (
	"unicode"
	"unicode/utf8"
)

// MaxSenderNameLength is the most Unicode code points AgentUserMessage.SenderName may have.
// Since 0.6.3.
const MaxSenderNameLength = 64

// IsValidSenderName reports whether a declared sender display name is valid.
//
// send_user_message refuses a message whose SenderName fails this check. Check a name a
// person typed with it before you send it, after you trim it. Every core applies the same
// rule. Since 0.6.3.
//
//	dexagents.IsValidSenderName("Alice")  // true
//	dexagents.IsValidSenderName(" Alice") // false
//
// It returns true when the name is valid UTF-8 with 1 to MaxSenderNameLength code points,
// contains no control character (Unicode category Cc) and no line or paragraph separator
// (Zl, Zp), and neither starts nor ends with a space separator (Zs); it returns false
// otherwise.
func IsValidSenderName(senderName string) bool {
	if !utf8.ValidString(senderName) {
		return false
	}
	runes := []rune(senderName)
	if len(runes) < 1 || len(runes) > MaxSenderNameLength {
		return false
	}
	for _, character := range runes {
		if unicode.In(character, unicode.Cc, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return !unicode.Is(unicode.Zs, runes[0]) && !unicode.Is(unicode.Zs, runes[len(runes)-1])
}

// BuildSenderNamedText returns the text the model sees for one user message.
//
// The session prefixes a message that has a sender name with that name, so the model knows
// who speaks when several people use one session. Since 0.6.3.
//
//	dexagents.BuildSenderNamedText(&name, "hello") // "Alice: hello" when name is "Alice"
//
// It returns "<senderName>: <text>" when senderName is non-nil, and text unchanged when it
// is nil.
func BuildSenderNamedText(senderName *string, text string) string {
	if senderName == nil {
		return text
	}
	return *senderName + ": " + text
}
