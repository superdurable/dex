// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dev

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The expected digests were computed independently with Python json.dumps(sort_keys=True, separators=(",", ":"),
// ensure_ascii=False) over the same selections.
func TestConnectorsMCPConnectionsConfigDigestMatchesFixedValues(t *testing.T) {
	for name, testCase := range map[string]struct {
		connections []string
		digest      string
	}{
		"tools null": {
			connections: []string{"slack/work"},
			digest:      "sha256:0037e3a418a9b55297c4b4ecc21e1b6ef98d6dd7069fcb2e474f9698b9076685",
		},
		"tools list and connection order": {
			connections: []string{"slack/work=post_thread_reply,list_thread_messages", "hacker-news-daily/default", "slack/personal"},
			digest:      "sha256:4e988684ec72ef1e36e4359dc473e712d2428832c1031ac7379adf41d9b749c7",
		},
		"same selection in another order": {
			connections: []string{"slack/personal", "hacker-news-daily/default", "slack/work=list_thread_messages,post_thread_reply"},
			digest:      "sha256:4e988684ec72ef1e36e4359dc473e712d2428832c1031ac7379adf41d9b749c7",
		},
		"duplicate tools": {
			connections: []string{"slack/work=postThreadReply,listThreadMessages,postThreadReply"},
			digest:      "sha256:52aa043f2763236906a9caa46763860c9630a58137f6b9bddac5062362e1feee",
		},
		"unicode, HTML characters, and escapes": {
			connections: []string{"gmail/个人邮箱=sendMessage", "slack/é<&> \"q\"\\\n\x01"},
			digest:      "sha256:998bd624c9049567406a108df2b3619f04d4353b308c376279f47c689e86ced7",
		},
		"byte order": {
			connections: []string{"slack/b=Z,a,_", "slack/B"},
			digest:      "sha256:130337f628d7b1a902d46512b37cc2c432c962ca3d8a3d420fa8355073f2ad31",
		},
	} {
		t.Run(name, func(t *testing.T) {
			digest, err := connectorsMCPConnectionsConfigDigest(testCase.connections)
			require.NoError(t, err)
			require.NotNil(t, digest)
			require.Equal(t, testCase.digest, *digest)
		})
	}
}

func TestConnectorsMCPConnectionsConfigDigestOfNoSelectionIsNull(t *testing.T) {
	digest, err := connectorsMCPConnectionsConfigDigest(nil)
	require.NoError(t, err)
	require.Nil(t, digest)
	require.True(t, isSameConnectionsConfigDigest(nil, digest), "null equals null")
	selected, err := connectorsMCPConnectionsConfigDigest([]string{"slack/work"})
	require.NoError(t, err)
	require.False(t, isSameConnectionsConfigDigest(nil, selected))
	require.False(t, isSameConnectionsConfigDigest(selected, nil))
}

func TestConnectorsMCPConnectionsConfigDigestRejectsDuplicateConnection(t *testing.T) {
	_, err := connectorsMCPConnectionsConfigDigest([]string{"slack/work", "slack/work=post_thread_reply"})
	require.ErrorContains(t, err, `connectors MCP connection "slack/work" is duplicated`)
}
