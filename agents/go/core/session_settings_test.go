// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A copy that an earlier turn left reads as no settings, so a turn that a Worker without session
// settings began never runs with the previous turn's model.
func TestATurnSettingsCopyFromAnEarlierTurnReadsAsNoSettings(t *testing.T) {
	stored := &TurnSessionSettings{TurnID: "turn-1", SessionSettings: map[string]any{"model": "alpha"}}
	turn1, turn2 := "turn-1", "turn-2"

	require.Equal(t, map[string]any{"model": "alpha"}, selectTurnSessionSettings(stored, &turn1))
	require.Equal(t, map[string]any{}, selectTurnSessionSettings(stored, &turn2))
	require.Equal(t, map[string]any{}, selectTurnSessionSettings(stored, nil))
	require.Equal(t, map[string]any{}, selectTurnSessionSettings(nil, &turn1))
}

// The conformance suite's size cases, with Go's own size where the cores differ, and its limits.
func TestSessionSettingsSizesMatchTheConformanceSuite(t *testing.T) {
	text, err := os.ReadFile(filepath.Join("..", "..", "conformance", "names.json"))
	require.NoError(t, err)
	var names struct {
		SessionSettingsSizes struct {
			Examples []struct {
				Name        string         `json:"name"`
				Settings    map[string]any `json:"settings"`
				Bytes       int            `json:"bytes"`
				BytesByCore map[string]int `json:"bytes_by_core"`
			} `json:"examples"`
			Limit []struct {
				Name                string `json:"name"`
				NoteLength          int    `json:"note_length"`
				Bytes               int    `json:"bytes"`
				IsAccepted          bool   `json:"is_accepted"`
				IsAcceptedBySession bool   `json:"is_accepted_by_session"`
			} `json:"limit"`
		} `json:"session_settings_sizes"`
		Limits map[string]int `json:"limits"`
	}
	require.NoError(t, json.Unmarshal(text, &names))
	require.Equal(t, MaxSessionSettingsBytes, names.Limits["max_session_settings_bytes"])
	require.Equal(t, SessionSettingsSizeToleranceBytes, names.Limits["session_settings_size_tolerance_bytes"])
	require.NotEmpty(t, names.SessionSettingsSizes.Examples)
	for _, example := range names.SessionSettingsSizes.Examples {
		expected, hasGoSize := example.BytesByCore["go"]
		if !hasGoSize {
			expected = example.Bytes
		}
		size, err := SessionSettingsSize(example.Settings)
		require.NoError(t, err)
		require.Equal(t, expected, size, example.Name)
	}
	for _, limitCase := range names.SessionSettingsSizes.Limit {
		settings := map[string]any{"note": strings.Repeat("x", limitCase.NoteLength)}
		size, err := SessionSettingsSize(settings)
		require.NoError(t, err)
		require.Equal(t, limitCase.Bytes, size, limitCase.Name)
		require.Equal(t, limitCase.IsAccepted, ValidateSessionSettings(settings) == nil, limitCase.Name)
		require.Equal(t, limitCase.IsAcceptedBySession, checkStoredSessionSettings(settings) == nil, limitCase.Name)
	}
}
