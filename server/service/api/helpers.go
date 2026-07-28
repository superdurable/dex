// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

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
