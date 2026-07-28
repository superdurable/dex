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

package integ

import (
	"github.com/superdurable/iwf/config"
	"github.com/superdurable/iwf/service"
)

const testNamespace = "default"

// Api.Port / fixed worker ports are unused: startWorker and startIwfService bind 127.0.0.1:0.

type IwfServiceTestConfig struct {
	BackendType     service.BackendType
	MemoEncryption  bool
	DefaultHeaders  map[string]string
	S3TestThreshold int
}

// integGrpcMaxMessageBytes must fit several hydrated 1MiB attributes on worker
// Invoke* RPCs (default gRPC 4MiB is too small for large_data_attributes_test).
const integGrpcMaxMessageBytes = 32 * 1024 * 1024

func createTestConfig(testCfg IwfServiceTestConfig) config.Config {
	cfg := config.Config{
		Api: config.ApiConfig{
			MaxWaitSeconds:      12, // use 12 so that we can test it in the waiting test
			GrpcMaxMessageBytes: integGrpcMaxMessageBytes,
			QueryWorkflowFailedRetryPolicy: config.QueryWorkflowFailedRetryPolicy{
				InitialIntervalSeconds: 1,
				MaximumAttempts:        10,
			},
		},
		Interpreter: config.Interpreter{
			VerboseDebug: false,
			InterpreterActivityConfig: config.InterpreterActivityConfig{
				DefaultHeaders: testCfg.DefaultHeaders,
			},
		},
	}
	switch testCfg.BackendType {
	case service.BackendTypeTemporal:
		cfg.Interpreter.Temporal = &config.TemporalConfig{}
	case service.BackendTypeCadence:
		cfg.Interpreter.Cadence = &config.CadenceConfig{}
	}
	if testCfg.S3TestThreshold > 0 {
		externalStorage := config.ExternalStorageConfig{
			Enabled:                     true,
			ThresholdInBytes:            testCfg.S3TestThreshold,
			MinAgeForCleanupCheckInDays: 3,
			SupportedStorages: []config.BlobStorageConfig{
				{
					Status:      config.StorageStatusActive,
					StorageId:   "s3-store-id",
					StorageType: config.StorageTypeS3,
					S3Endpoint:  "http://localhost:9000",
					S3Bucket:    "iwf-test-bucket",
					S3Region:    "us-east-1",
					S3AccessKey: "minioadmin",
					S3SecretKey: "minioadmin",
				},
			},
		}
		cfg.ExternalStorage = externalStorage
	}
	return cfg
}
