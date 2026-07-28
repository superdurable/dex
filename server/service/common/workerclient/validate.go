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

package workerclient

import (
	"fmt"

	"github.com/superdurable/dex/gen/dexpb"
)

// RejectWorkerBlobIDs rejects server-minted blob-id arms on worker responses (untrusted).
func RejectWorkerBlobIDs(values ...*dexpb.Value) error {
	for _, value := range values {
		if value == nil {
			continue
		}
		switch value.GetKind().(type) {
		case *dexpb.Value_InternalBlobIdForStringValue, *dexpb.Value_InternalBlobIdForObjValue:
			return fmt.Errorf("worker response must not contain internal_blob_id arms")
		}
	}
	return nil
}

// RejectWorkerAttributeWriteBlobIDs rejects blob-id arms on AttributeWrite values.
func RejectWorkerAttributeWriteBlobIDs(writes []*dexpb.AttributeWrite) error {
	for _, write := range writes {
		if write == nil {
			continue
		}
		if err := RejectWorkerBlobIDs(write.GetValue()); err != nil {
			return err
		}
	}
	return nil
}

// RejectWorkerKVBlobIDs rejects blob-id arms on KV values.
func RejectWorkerKVBlobIDs(kvs []*dexpb.KV) error {
	for _, kv := range kvs {
		if kv == nil {
			continue
		}
		if err := RejectWorkerBlobIDs(kv.GetValue()); err != nil {
			return err
		}
	}
	return nil
}
