// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package blobstore

import (
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/service/common/log/loggerimpl"
	"github.com/superdurable/dex/service/common/ptr"
	"go.temporal.io/sdk/client"
)

func TestBlobStoreRejectsReservedSuperVerseNamespace(t *testing.T) {
	logger, err := loggerimpl.NewDevelopment()
	require.NoError(t, err)
	_, err = NewBlobStore(nil, "_superverse", &config.BlobStoreConfig{
		Enabled: ptr.Any(true),
		SupportedStorages: []config.BlobStoreConfigEntry{{
			Status: config.StorageStatusActive, StorageId: "local",
			StorageType: config.StorageTypeLocal, LocalDirectory: t.TempDir(),
		}},
	}, logger, client.MetricsNopHandler)
	require.ErrorContains(t, err, "reserved")
}

func TestDeterministicBlobObjectIDStableVector(t *testing.T) {
	objectID, err := deterministicBlobObjectID("run-123activity-456", []byte("payload"), 10)
	require.NoError(t, err)
	require.Equal(t, "8d1zvkzfui", objectID)
}

func TestDeterministicBlobObjectIDLengthsAndAlphabet(t *testing.T) {
	for _, objectIDLength := range []int{1, 9, 10, 12, 16, 22, 50, 51} {
		objectID, err := deterministicBlobObjectID("request", []byte("payload"), objectIDLength)
		require.NoError(t, err)
		require.Len(t, objectID, objectIDLength)
		require.True(t, regexp.MustCompile(`^[0-9a-z]+$`).MatchString(objectID))
	}
}

func TestDeterministicBlobObjectIDFramesComponents(t *testing.T) {
	firstID, err := deterministicBlobObjectID("ab", []byte("c"), 10)
	require.NoError(t, err)
	secondID, err := deterministicBlobObjectID("a", []byte("bc"), 10)
	require.NoError(t, err)
	require.NotEqual(t, firstID, secondID)
}

func TestDeterministicBlobObjectIDValuePathUsesContextualFlow(t *testing.T) {
	path, err := ValueObjectPath("coding-session--c08256c4", "c/0abcde1234")
	require.NoError(t, err)
	require.Equal(t, "260913$coding-session--c08256c4/0abcde1234", path)

	for _, locator := range []string{
		"c/",
		"c/ABCDEF1234",
		"c/abcdef1234/extra",
		"C/abcdef1234",
		"00/abcdef1234",
		"260913/abcdef1234",
		"ko1/abcdef1234",
	} {
		_, err := ValueObjectPath("flow", locator)
		require.Error(t, err)
	}
}

func TestDeterministicBlobObjectIDReferenceDateVectors(t *testing.T) {
	minusSevenHours := time.FixedZone("UTC-7", -7*60*60)
	testCases := []struct {
		date time.Time
		code string
	}{
		{date: time.Date(2026, time.September, 1, 23, 59, 59, 0, time.UTC), code: "0"},
		{date: time.Date(2026, time.September, 12, 16, 59, 59, 0, minusSevenHours), code: "b"},
		{date: time.Date(2026, time.September, 12, 17, 0, 0, 0, minusSevenHours), code: "c"},
		{date: time.Date(2026, time.September, 13, 0, 0, 0, 0, time.UTC), code: "c"},
		{date: time.Date(2026, time.October, 6, 0, 0, 0, 0, time.UTC), code: "z"},
		{date: time.Date(2026, time.October, 7, 0, 0, 0, 0, time.UTC), code: "10"},
		{date: time.Date(2099, time.December, 31, 0, 0, 0, 0, time.UTC), code: "ko0"},
	}

	for _, testCase := range testCases {
		code, err := formatBlobReferenceDate(testCase.date)
		require.NoError(t, err)
		require.Equal(t, testCase.code, code)
		parsed, err := parseBlobReferenceDate(code)
		require.NoError(t, err)
		require.Equal(t, testCase.date.UTC().Format(time.DateOnly), parsed.Format(time.DateOnly))
	}
}

func TestDeterministicBlobObjectIDReferenceDateRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "00", "C", "-1", "260913", "ko1", "!"} {
		_, err := parseBlobReferenceDate(value)
		require.Error(t, err, value)
	}

	_, err := formatBlobReferenceDate(time.Date(2026, time.August, 31, 0, 0, 0, 0, time.UTC))
	require.Error(t, err)
	_, err = formatBlobReferenceDate(time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC))
	require.Error(t, err)
}

func TestDeterministicBlobObjectIDFlowIDPathPartRoundTrip(t *testing.T) {
	testCases := map[string]string{
		"coding-session--c08256c4": "coding-session--c08256c4",
		"SubFlow:parent-step-0":    "SubFlow%3Aparent-step-0",
		"percent%flow":             "percent%25flow",
		`backslash\flow`:           "backslash%5Cflow",
		"slash/flow":               "slash%2Fflow",
		"line\nbreak":              "line%0Abreak",
		"flow.with spaces":         "flow%2Ewith%20spaces",
		"订单":                       "%E8%AE%A2%E5%8D%95",
	}

	for flowID, expected := range testCases {
		encoded := encodeFlowIDPathPart(flowID)
		require.Equal(t, expected, encoded)
		decoded, err := decodeFlowIDPathPart(encoded)
		require.NoError(t, err)
		require.Equal(t, flowID, decoded)
	}
}

func TestDeterministicBlobObjectIDFlowIDPathPartRejectsNonCanonicalValues(t *testing.T) {
	for _, value := range []string{"", "%", "%3", "%3a", "%41", "flow.name", "订单"} {
		_, err := decodeFlowIDPathPart(value)
		require.Error(t, err, value)
	}
}

func TestDeterministicBlobObjectIDWorkflowPathDoesNotDecodeLegacyBase64FlowID(t *testing.T) {
	parsed, err := ParseWorkflowPath("260913$Y29kaW5nLXNlc3Npb24")
	require.NoError(t, err)
	require.Equal(t, "Y29kaW5nLXNlc3Npb24", parsed.FlowID)
}
