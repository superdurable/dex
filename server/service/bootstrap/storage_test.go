// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
)

func TestFindS3StorageRequiresExistingS3Entry(t *testing.T) {
	cfg := &config.Config{BlobStore: config.BlobStoreConfig{SupportedStorages: []config.BlobStoreConfigEntry{
		{StorageId: "local", StorageType: config.StorageTypeLocal},
		{StorageId: "p0", StorageType: config.StorageTypeS3, S3Bucket: "definitions"},
	}}}
	storage, err := FindS3Storage(cfg, "p0")
	require.NoError(t, err)
	require.Equal(t, "definitions", storage.S3Bucket)

	_, err = FindS3Storage(cfg, "missing")
	require.ErrorContains(t, err, "was not found")
	_, err = FindS3Storage(cfg, "local")
	require.ErrorContains(t, err, "is not S3")
}
