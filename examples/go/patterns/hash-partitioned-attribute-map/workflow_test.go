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

package hashpartitionedattributemap

import "testing"

func TestEmailPartitionGoldenVectors(t *testing.T) {
	tests := []struct {
		emailAddress   string
		canonicalEmail string
		hash           uint32
		partitionName  string
	}{
		{" Alice@Example.COM ", "alice@example.com", 2493822278, "partition-278"},
		{"bob@example.com", "bob@example.com", 3055529145, "partition-145"},
		{"support+west@example.org", "support+west@example.org", 2156001632, "partition-632"},
	}
	for _, test := range tests {
		canonicalEmail, partitionName, hash, err := EmailPartition(test.emailAddress)
		if err != nil {
			t.Fatalf("EmailPartition(%q): %v", test.emailAddress, err)
		}
		if canonicalEmail != test.canonicalEmail || hash != test.hash || partitionName != test.partitionName {
			t.Fatalf(
				"EmailPartition(%q) = (%q, %d, %q)",
				test.emailAddress,
				canonicalEmail,
				hash,
				partitionName,
			)
		}
	}
}

func TestCanonicalEmailAddressRejectsInvalidInput(t *testing.T) {
	for _, emailAddress := range []string{"", " \t\r\n", "josé@example.com"} {
		if _, err := CanonicalEmailAddress(emailAddress); err == nil {
			t.Fatalf("CanonicalEmailAddress(%q) succeeded", emailAddress)
		}
	}
}
