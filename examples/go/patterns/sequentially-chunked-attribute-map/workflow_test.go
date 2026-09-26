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

package sequentiallychunkedattributemap

import "testing"

func TestValidateSubscriberPageToken(t *testing.T) {
	validTokens := map[string]string{
		"":                     CurrentChunkInstance,
		CurrentChunkInstance:   CurrentChunkInstance,
		"00000000000000000001": "00000000000000000001",
		"00000000000000000101": "00000000000000000101",
	}
	for input, expected := range validTokens {
		actual, err := ValidateSubscriberPageToken(input)
		if err != nil || actual != expected {
			t.Fatalf("ValidateSubscriberPageToken(%q) = (%q, %v)", input, actual, err)
		}
	}
	for _, token := range []string{"1", "archive", "00000000000000000002"} {
		if _, err := ValidateSubscriberPageToken(token); err == nil {
			t.Fatalf("ValidateSubscriberPageToken(%q) succeeded", token)
		}
	}
}
