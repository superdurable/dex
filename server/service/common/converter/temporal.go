// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package converter

import (
	"go.temporal.io/sdk/converter"
)

// NewTemporalDataConverter returns a binary-protobuf-first Temporal DataConverter.
// JSON remains only as an escape hatch for non-proto SDK values.
func NewTemporalDataConverter() converter.DataConverter {
	return converter.NewCompositeDataConverter(
		converter.NewNilPayloadConverter(),
		converter.NewByteSlicePayloadConverter(),
		converter.NewProtoPayloadConverter(),
		converter.NewProtoJSONPayloadConverter(),
		converter.NewJSONPayloadConverter(),
	)
}
