// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"encoding/json"
	"testing"
)

func TestGmailTriggerBindingConfigurationValidatesTypedMatchers(t *testing.T) {
	valid := map[string]json.RawMessage{
		"searchQuery":  json.RawMessage(`"label:inbox"`),
		"replyMatcher": json.RawMessage(`{"messageContains":"approved","senderEmails":["Approver <approver@example.com>"]}`),
	}
	if err := validateConnectorTriggerBindingConfiguration("gmail", "replyReceived", valid); err != nil {
		t.Fatal(err)
	}
	invalid := map[string]json.RawMessage{
		"replyMatcher": json.RawMessage(`{"senderEmails":["not-an-email"]}`),
	}
	if err := validateConnectorTriggerBindingConfiguration("gmail", "replyReceived", invalid); err == nil {
		t.Fatal("invalid sender email was accepted")
	}
	unknown := map[string]json.RawMessage{"rpcName": json.RawMessage(`"ReceiveEmailReply"`)}
	if err := validateConnectorTriggerBindingConfiguration("gmail", "replyReceived", unknown); err == nil {
		t.Fatal("configurable RPC name was accepted")
	}
}
