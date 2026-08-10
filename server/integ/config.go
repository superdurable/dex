// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/service"
)

const testNamespace = "default"

// Api.Port / fixed worker ports are unused: startWorker and startDexService bind 127.0.0.1:0.

type DexServiceTestConfig struct {
	BackendType        service.BackendType
	MemoEncryption     bool
	DefaultHeaders     map[string]string
	S3TestThreshold    int
	LocalBlobDirectory string
	LocalBlobThreshold int
	AttributeStore     config.AttributeStoreConfig
	BlobCacheDirectory string
	// LazyLoading overrides BlobStore.LazyLoading when S3 is enabled.
	// Nil uses EffectiveLazyLoading default (true).
	LazyLoading *bool
}

func createTestConfig(testCfg DexServiceTestConfig) config.Config {
	cfg := config.Config{
		Api: config.ApiConfig{
			MaxWaitSeconds: 12, // use 12 so that we can test it in the waiting test
			QueryWorkflowFailedRetryPolicy: config.QueryWorkflowFailedRetryPolicy{
				InitialIntervalSeconds: 1,
				MaximumAttempts:        10,
			},
		},
		Worker: config.WorkerConfig{
			DefaultHeaders: testCfg.DefaultHeaders,
		},
		Interpreter: config.Interpreter{
			DefaultWorkflowConfig: syncDurabilityConfig(),
			VerboseDebug:          false,
		},
	}
	cfg.AttributeStore = testCfg.AttributeStore
	switch testCfg.BackendType {
	case service.BackendTypeTemporal:
		cfg.Interpreter.Temporal = &config.TemporalConfig{}
	case service.BackendTypeCadence:
		cfg.Interpreter.Cadence = &config.CadenceConfig{}
	}
	if testCfg.S3TestThreshold > 0 {
		blobStoreCfg := config.BlobStoreConfig{
			Enabled:                true,
			LazyLoading:            testCfg.LazyLoading,
			ThresholdInBytes:       testCfg.S3TestThreshold,
			HistoryRetentionInDays: 3,
			BlobCache: config.BlobCacheConfig{
				Directory: testCfg.BlobCacheDirectory,
			},
			SupportedStorages: []config.BlobStoreConfigEntry{
				{
					Status:      config.StorageStatusActive,
					StorageId:   "s3-store-id",
					StorageType: config.StorageTypeS3,
					S3Endpoint:  "http://localhost:9000",
					S3Bucket:    "dex-test-bucket",
					S3Region:    "us-east-1",
					S3AccessKey: "minioadmin",
					S3SecretKey: "minioadmin",
				},
			},
		}
		cfg.BlobStore = blobStoreCfg
	}
	if testCfg.LocalBlobDirectory != "" {
		cfg.BlobStore = config.BlobStoreConfig{
			Enabled:                true,
			LazyLoading:            testCfg.LazyLoading,
			ThresholdInBytes:       testCfg.LocalBlobThreshold,
			HistoryRetentionInDays: 3,
			SupportedStorages: []config.BlobStoreConfigEntry{{
				Status:         config.StorageStatusActive,
				StorageId:      "local-store-id",
				StorageType:    config.StorageTypeLocal,
				LocalDirectory: testCfg.LocalBlobDirectory,
			}},
		}
	}
	return cfg
}
