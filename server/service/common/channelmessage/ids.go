// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package channelmessage

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/superdurable/dex/gen/dexpb"
)

// AssignIDs replaces every message ID with a new UUIDv7.
func AssignIDs(messages []*dexpb.ChannelMessage) error {
	for index, message := range messages {
		if message == nil {
			return fmt.Errorf("Channel publication at index %d is nil", index)
		}
		messageID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		message.MessageId = messageID.String()
	}
	return nil
}
