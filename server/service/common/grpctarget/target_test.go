// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

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
