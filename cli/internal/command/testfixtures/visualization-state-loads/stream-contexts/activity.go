// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package streamcontexts

import "github.com/superdurable/dex/sdk-go/dex"

func (*ActivityFlow) writeActivitySnapshot(ctx dex.Context) error {
	return writeStreamActivity(ctx, Activity, "snapshot changed")
}

func writeStreamActivity(ctx dex.Context, stream dex.Stream[string], message string) error {
	return stream.Write(ctx, message)
}

func writeBufferedActivity(ctx dex.Context, stream dex.Stream[string]) error {
	writer, err := dex.NewBufferedTextStream(ctx, stream)
	if err != nil {
		return err
	}
	return writeBufferedChunk(writer)
}

func writeBufferedChunk(writer *dex.BufferedTextStream) error {
	return writer.Write("buffered activity")
}
