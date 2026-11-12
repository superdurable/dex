// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package sessionfeed

import "encoding/json"

// ImageAttachment is one stored image of a user message.
type ImageAttachment struct {
	// Name is the file's base name for the chip, such as "diagram.png".
	Name string
	// MimeType is the image type, such as "image/png".
	MimeType string
	// SizeBytes is the image size.
	SizeBytes int64
	// Reference is the session's image reference JSON, passed back unchanged.
	Reference json.RawMessage
}
