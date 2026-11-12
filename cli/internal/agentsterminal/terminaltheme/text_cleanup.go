// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package terminaltheme

import "strings"

// ReplaceControlCharacters turns every C0 and C1 control character into a
// space.
func ReplaceControlCharacters(text string) string {
	return strings.Map(func(character rune) rune {
		if isControlCharacter(character) {
			return ' '
		}
		return character
	}, text)
}

// ReplaceControlCharactersKeepingLines is ReplaceControlCharacters for
// multi-line text: it keeps newlines and tabs and drops carriage returns.
func ReplaceControlCharactersKeepingLines(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.Map(func(character rune) rune {
		if character == '\n' || character == '\t' || !isControlCharacter(character) {
			return character
		}
		return ' '
	}, text)
}

// TruncateCodePoints returns text, or its first limit-1 code points and
// ellipsis when it has more than limit code points.
func TruncateCodePoints(text string, limit int, ellipsis string) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + ellipsis
}

func isControlCharacter(character rune) bool {
	return character <= 0x1f || (character >= 0x7f && character <= 0x9f)
}
