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

package grpctarget

import (
	"testing"

	"github.com/superdurable/dex/gen/dexpb"
)

func TestNormalizeWorkerTargetRejectsHTTP(t *testing.T) {
	_, err := NormalizeWorkerTarget(&dexpb.WorkerTarget{Address: "http://localhost:8080"})
	if err == nil {
		t.Fatal("expected error for http URL")
	}
	_, err = NormalizeWorkerTarget(&dexpb.WorkerTarget{Address: "https://worker.example:443"})
	if err == nil {
		t.Fatal("expected error for https URL")
	}
}

func TestNormalizeWorkerTargetAcceptsHostPort(t *testing.T) {
	got, err := NormalizeWorkerTarget(&dexpb.WorkerTarget{Address: "  127.0.0.1:9000  "})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetAddress() != "127.0.0.1:9000" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeWorkerTargetEmpty(t *testing.T) {
	_, err := NormalizeWorkerTarget(&dexpb.WorkerTarget{Address: "   "})
	if err == nil {
		t.Fatal("expected empty error")
	}
}
