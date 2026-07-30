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

package blobstore

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestDeterministicBlobUUIDStableVector(t *testing.T) {
	objectID, err := deterministicBlobUUID("run-123activity-456", []byte("payload"))
	require.NoError(t, err)
	require.Equal(t, "d3333fb9-f46d-8815-8359-9892fd394a6e", objectID.String())
}

func TestDeterministicBlobUUIDVersionAndVariant(t *testing.T) {
	objectID, err := deterministicBlobUUID("request", []byte("payload"))
	require.NoError(t, err)
	require.Equal(t, uuid.Version(8), objectID.Version())
	require.Equal(t, uuid.RFC4122, objectID.Variant())
}

func TestDeterministicBlobUUIDFramesComponents(t *testing.T) {
	firstID, err := deterministicBlobUUID("ab", []byte("c"))
	require.NoError(t, err)
	secondID, err := deterministicBlobUUID("a", []byte("bc"))
	require.NoError(t, err)
	require.NotEqual(t, firstID, secondID)
}
