// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexagents

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// MaxSessionSettingsBytes is the largest session settings, as the UTF-8 bytes of their compact
// JSON text: 16 KiB. It is the same in every core.
const MaxSessionSettingsBytes = 16 * 1024

// SessionSettingsSizeToleranceBytes is how many bytes over MaxSessionSettingsBytes the session's
// own Steps and RPC accept. The cores write some numbers and characters differently, so settings
// that one core's Client accepted can measure a few bytes more in another core's Worker; the
// tolerance keeps that from failing a session. ValidateSessionSettings checks the exact limit.
const SessionSettingsSizeToleranceBytes = 1024

// InvalidStartSessionSettingsReason starts the Flow failure reason when StartAgentSession refuses
// the start input's settings.
const InvalidStartSessionSettingsReason = "dex-agents: the start input's session_settings are not valid session settings"

// SessionSettingsSize returns the size of session settings that MaxSessionSettingsBytes limits.
//
// The size is the UTF-8 byte length of the compact JSON text encoding/json
// writes, with <, >, and & not escaped. The Python and TypeScript cores
// measure their own JSON text, which is the same for strings, integers,
// booleans, null, objects, and arrays, with two exceptions: Go escapes U+2028
// and U+2029 as \u2028 and \u2029, which the other cores write as themselves,
// and Python writes floats in their shortest form, such as 1.0 and 1e+16, where
// Go and TypeScript write 1 and 10000000000000000. So sizes can differ by a few
// bytes per such character or float. The session's Steps and RPC accept
// SessionSettingsSizeToleranceBytes more than the limit, so settings that
// another core's Client accepted never fail a session.
//
//	size, err := dexagents.SessionSettingsSize(map[string]any{"model": "gpt-5-mini"}) // 22
//
// It returns an error when the settings cannot be written as JSON.
func SessionSettingsSize(sessionSettings map[string]any) (int, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(sessionSettings); err != nil {
		return 0, fmt.Errorf("session settings must be JSON: %w", err)
	}
	// Encode ends the text with a newline that is not part of the JSON.
	return len(bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))), nil
}

// ValidateSessionSettings checks that a value can be a session's settings.
//
// AgentSessionClient.StartSessionWithSettings and
// AgentSessionClient.UpdateSessionSettings call it before they send anything.
// Call it before Client.StartFlow or the RPC to report a problem early: a start
// whose settings the session refuses fails the session at once. Nil means no
// settings and is valid.
//
// It returns an error when the settings cannot be written as JSON or are
// larger than MaxSessionSettingsBytes.
func ValidateSessionSettings(sessionSettings map[string]any) error {
	return checkSessionSettings(sessionSettings, MaxSessionSettingsBytes)
}

// checkStoredSessionSettings checks settings as the session's Steps and RPC do, with the tolerance.
func checkStoredSessionSettings(sessionSettings map[string]any) error {
	return checkSessionSettings(sessionSettings, MaxSessionSettingsBytes+SessionSettingsSizeToleranceBytes)
}

func checkSessionSettings(sessionSettings map[string]any, limitBytes int) error {
	size, err := SessionSettingsSize(sessionSettings)
	if err != nil {
		return err
	}
	if size > limitBytes {
		return fmt.Errorf("session settings are %d bytes, more than the %d allowed", size, limitBytes)
	}
	return nil
}

// selectTurnSessionSettings returns the stored copy's settings while it belongs to the active turn, else
// an empty map. A turn that a Worker without session settings began leaves an earlier turn's copy.
func selectTurnSessionSettings(stored *TurnSessionSettings, activeTurnID *string) map[string]any {
	if stored == nil || activeTurnID == nil || stored.TurnID != *activeTurnID || stored.SessionSettings == nil {
		return map[string]any{}
	}
	return stored.SessionSettings
}
