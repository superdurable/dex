// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"
)

type gmailMessageMatcherConfiguration struct {
	MessageContains string   `json:"messageContains,omitempty"`
	SenderEmails    []string `json:"senderEmails,omitempty"`
}

type gmailMessageReceivedTriggerConfiguration struct {
	SearchQuery    string                           `json:"searchQuery,omitempty"`
	MessageMatcher gmailMessageMatcherConfiguration `json:"messageMatcher,omitempty"`
}

type gmailReplyReceivedTriggerConfiguration struct {
	SearchQuery  string                           `json:"searchQuery,omitempty"`
	ReplyMatcher gmailMessageMatcherConfiguration `json:"replyMatcher,omitempty"`
}

func validateGmailTriggerBindingConfiguration(triggerName string, configuration map[string]json.RawMessage) error {
	contents, err := json.Marshal(configuration)
	if err != nil {
		return fmt.Errorf("Gmail Trigger configuration is invalid")
	}
	switch triggerName {
	case "messageReceived":
		var decoded gmailMessageReceivedTriggerConfiguration
		if err := decodeStrictConnectorJSONReader(bytes.NewReader(contents), &decoded); err != nil {
			return fmt.Errorf("Gmail Trigger configuration is invalid")
		}
		return validateGmailSenderEmails(decoded.MessageMatcher.SenderEmails)
	case "replyReceived":
		var decoded gmailReplyReceivedTriggerConfiguration
		if err := decodeStrictConnectorJSONReader(bytes.NewReader(contents), &decoded); err != nil {
			return fmt.Errorf("Gmail Trigger configuration is invalid")
		}
		return validateGmailSenderEmails(decoded.ReplyMatcher.SenderEmails)
	default:
		return fmt.Errorf("Gmail Trigger is unsupported")
	}
}

func validateGmailSenderEmails(senderEmails []string) error {
	seen := make(map[string]bool, len(senderEmails))
	for _, value := range senderEmails {
		address, err := mail.ParseAddress(value)
		if err != nil || address.Address == "" {
			return fmt.Errorf("Gmail sender email is invalid")
		}
		canonical := strings.ToLower(address.Address)
		if seen[canonical] {
			return fmt.Errorf("Gmail sender emails must be unique")
		}
		seen[canonical] = true
	}
	return nil
}
