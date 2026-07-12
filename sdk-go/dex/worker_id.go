package dex

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"time"
)

// generateWorkerID builds the per-Worker identifier the server uses for
// WorkerID exclusivity (CAS on RunRow.WorkerID) and sticky external-events
// routing. Format: "<hostID>-<startTimeISO>-<rand6>".
func generateWorkerID(hostID string) string {
	if hostID == "" {
		hostID = os.Getenv("HOSTNAME")
	}
	if hostID == "" {
		hostID = "host-unknown"
	}
	startISO := time.Now().UTC().Format(time.RFC3339Nano)
	var b [3]byte
	_, _ = rand.Read(b[:])
	return hostID + "-" + startISO + "-" + hex.EncodeToString(b[:])
}
