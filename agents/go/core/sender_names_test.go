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
	"github.com/superdurable/dex/agents/go/core/internal/pythonjson"
)

func TestSenderNameRuleCountsCodePointsAndRefusesBreaks(t *testing.T) {
	t.Parallel()
	require.True(t, IsValidSenderName("Alice"))
	require.True(t, IsValidSenderName(strings.Repeat("\U0001F600", 64)))
	require.True(t, IsValidSenderName("\ufeffAlice"))
	require.False(t, IsValidSenderName(strings.Repeat("\U0001F600", 65)))
	for _, name := range []string{"", " Alice", "Alice ", "A\nB", "A\u2028B", "\u0007", strings.Repeat("x", 65), "\xff"} {
		require.False(t, IsValidSenderName(name), "%q", name)
	}
}

// Conformance user messages and steer requests round-trip with their sender names.
func TestSenderNameConformanceValuesRoundTrip(t *testing.T) {
	t.Parallel()
	senderNames := map[string]*string{}
	roundTrip := func(fileName string, decode func(wire []byte) (any, *string, error)) {
		text, err := os.ReadFile(filepath.Join("..", "..", "conformance", "values", fileName))
		require.NoError(t, err)
		var document struct {
			Cases []struct {
				Name      string `json:"name"`
				Wire      string `json:"wire"`
				Canonical string `json:"canonical"`
			} `json:"cases"`
		}
		require.NoError(t, json.Unmarshal(text, &document))
		for _, testCase := range document.Cases {
			value, senderName, err := decode([]byte(testCase.Wire))
			require.NoError(t, err, testCase.Name)
			encoded, err := json.Marshal(value)
			require.NoError(t, err, testCase.Name)
			canonical, err := pythonjson.Marshal(json.RawMessage(encoded),
				pythonjson.Options{IsCompact: true, ShouldSortKeys: true, ShouldEscapeNonASCII: true})
			require.NoError(t, err, testCase.Name)
			require.Equal(t, testCase.Canonical, canonical, fileName+" "+testCase.Name)
			senderNames[fileName+" "+testCase.Name] = senderName
		}
	}
	roundTrip("AgentUserMessage.json", func(wire []byte) (any, *string, error) {
		var message AgentUserMessage
		err := json.Unmarshal(wire, &message)
		return message, message.SenderName, err
	})
	roundTrip("SteerUserMessageRequest.json", func(wire []byte) (any, *string, error) {
		var request SteerUserMessageRequest
		err := json.Unmarshal(wire, &request)
		return request, request.SenderName, err
	})
	roundTrip("AgentMessage.json", func(wire []byte) (any, *string, error) {
		var message AgentMessage
		err := json.Unmarshal(wire, &message)
		return message, message.SenderName, err
	})
	require.Equal(t, ptr("Åsa \U0001F600"), senderNames["AgentUserMessage.json sender-name"])
	require.Equal(t, ptr("Alice"), senderNames["SteerUserMessageRequest.json by-text-with-sender-name"])
	require.Equal(t, ptr("Alice"), senderNames["AgentMessage.json sender-name"])
	require.Contains(t, senderNames, "AgentMessage.json explicit-null-sender-name")
	require.Nil(t, senderNames["AgentMessage.json explicit-null-sender-name"])
}

func TestSenderNamedTextMatchesTheConformanceSuite(t *testing.T) {
	t.Parallel()
	text, err := os.ReadFile(filepath.Join("..", "..", "conformance", "texts.json"))
	require.NoError(t, err)
	var texts struct {
		Templates map[string][]struct {
			Input struct {
				SenderName *string `json:"sender_name"`
				Text       string  `json:"text"`
			} `json:"input"`
			Text string `json:"text"`
		} `json:"templates"`
	}
	require.NoError(t, json.Unmarshal(text, &texts))
	cases := texts.Templates["sender_named_user_message"]
	require.NotEmpty(t, cases)
	for _, testCase := range cases {
		require.Equal(t, testCase.Text, BuildSenderNamedText(testCase.Input.SenderName, testCase.Input.Text))
	}
}
