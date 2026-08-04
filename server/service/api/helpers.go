// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package api

import (
	"fmt"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/workerclient"
)

func validateAttributeWrites(attributes []*dexpb.AttributeWrite) error {
	seenKeys := make(map[string]bool, len(attributes))
	for index, attribute := range attributes {
		if attribute == nil || attribute.GetKey() == "" || attribute.GetValue() == nil ||
			attribute.GetValue().GetKind() == nil {
			return fmt.Errorf("attribute %d is invalid", index)
		}
		if seenKeys[attribute.GetKey()] {
			return fmt.Errorf("attribute keys must be unique")
		}
		seenKeys[attribute.GetKey()] = true
		if err := workerclient.RejectWorkerBlobIDs(attribute.GetValue()); err != nil {
			return err
		}
	}
	return nil
}
